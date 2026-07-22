package middleware

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrConsumed       = errors.New("envelope consumed")
	ErrDirectionBusy  = errors.New("pipeline direction is exclusively owned")
	ErrPipelineClosed = errors.New("pipeline is closed")
)

type Direction uint8

const (
	Inbound Direction = iota
	Outbound
)

type Envelope struct {
	Data      []byte
	Direction Direction
	At        time.Time
	Source    string
}

type Action struct {
	Envelope Envelope
	Consumed bool
}

func Forward(envelope Envelope) Action { return Action{Envelope: envelope} }
func Consume() Action                  { return Action{Consumed: true} }

type Handler interface{ Name() string }
type InboundHandler interface {
	HandleInbound(context.Context, Envelope) (Action, error)
}
type OutboundHandler interface {
	HandleOutbound(context.Context, Envelope) (Action, error)
}
type Starter interface{ Start(context.Context) error }
type Stopper interface{ Stop(context.Context) error }

type Capability uint8

const (
	Passive Capability = iota
	ConsumeInbound
	ExclusiveOutbound
	ExclusiveDuplex
)

type capable interface{ Capability() Capability }

type Pipeline struct {
	mu            sync.RWMutex
	handlers      []Handler
	inboundOwner  string
	outboundOwner string
	closed        bool
}

func NewPipeline(handlers ...Handler) (*Pipeline, error) {
	p := &Pipeline{}
	for _, handler := range handlers {
		if err := p.Add(context.Background(), handler); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (p *Pipeline) Add(ctx context.Context, handler Handler) error {
	if handler == nil || handler.Name() == "" {
		return errors.New("middleware handler name is required")
	}
	if starter, ok := handler.(Starter); ok {
		if err := starter.Start(ctx); err != nil {
			return fmt.Errorf("start handler %q: %w", handler.Name(), err)
		}
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		p.stopAfterRejectedAdd(ctx, handler)
		return ErrPipelineClosed
	}
	for _, existing := range p.handlers {
		if existing.Name() == handler.Name() {
			p.mu.Unlock()
			p.stopAfterRejectedAdd(ctx, handler)
			return fmt.Errorf("middleware handler %q already exists", handler.Name())
		}
	}
	if err := p.claimLocked(handler); err != nil {
		p.mu.Unlock()
		p.stopAfterRejectedAdd(ctx, handler)
		return err
	}
	p.handlers = append(p.handlers, handler)
	p.mu.Unlock()
	return nil
}

func (p *Pipeline) stopAfterRejectedAdd(ctx context.Context, handler Handler) {
	if stopper, ok := handler.(Stopper); ok {
		_ = stopper.Stop(ctx)
	}
}

func (p *Pipeline) claimLocked(handler Handler) error {
	capability := Passive
	if value, ok := handler.(capable); ok {
		capability = value.Capability()
	}
	name := handler.Name()
	switch capability {
	case ExclusiveOutbound:
		if p.outboundOwner != "" {
			return fmt.Errorf("%w: outbound owned by %s", ErrDirectionBusy, p.outboundOwner)
		}
		p.outboundOwner = name
	case ExclusiveDuplex:
		if p.inboundOwner != "" || p.outboundOwner != "" {
			return fmt.Errorf("%w", ErrDirectionBusy)
		}
		p.inboundOwner, p.outboundOwner = name, name
	case ConsumeInbound:
		if p.inboundOwner != "" {
			return fmt.Errorf("%w: inbound owned by %s", ErrDirectionBusy, p.inboundOwner)
		}
		p.inboundOwner = name
	}
	return nil
}

func (p *Pipeline) Remove(ctx context.Context, name string) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrPipelineClosed
	}
	index := -1
	var handler Handler
	for i, candidate := range p.handlers {
		if candidate.Name() == name {
			index, handler = i, candidate
			break
		}
	}
	if index < 0 {
		p.mu.Unlock()
		return fmt.Errorf("middleware handler %q not found", name)
	}
	p.handlers = append(p.handlers[:index], p.handlers[index+1:]...)
	if p.inboundOwner == name {
		p.inboundOwner = ""
	}
	if p.outboundOwner == name {
		p.outboundOwner = ""
	}
	p.mu.Unlock()
	if stopper, ok := handler.(Stopper); ok {
		if err := stopper.Stop(ctx); err != nil {
			return fmt.Errorf("stop handler %q: %w", name, err)
		}
	}
	return nil
}

func (p *Pipeline) ProcessInbound(ctx context.Context, envelope Envelope) (Envelope, error) {
	envelope.Direction = Inbound
	envelope.Data = append([]byte(nil), envelope.Data...)
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return Envelope{}, ErrPipelineClosed
	}
	handlers := p.handlers
	for _, handler := range handlers {
		inbound, ok := handler.(InboundHandler)
		if !ok {
			continue
		}
		action, err := inbound.HandleInbound(ctx, envelope)
		if err != nil {
			return Envelope{}, fmt.Errorf("middleware %s inbound: %w", handler.Name(), err)
		}
		if action.Consumed {
			return Envelope{}, ErrConsumed
		}
		envelope = action.Envelope
		envelope.Direction = Inbound
	}
	return envelope, nil
}

func (p *Pipeline) ProcessOutbound(ctx context.Context, envelope Envelope) (Envelope, error) {
	envelope.Direction = Outbound
	envelope.Data = append([]byte(nil), envelope.Data...)
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return Envelope{}, ErrPipelineClosed
	}
	handlers := p.handlers
	owner := p.outboundOwner
	if owner != "" && envelope.Source != owner {
		return Envelope{}, fmt.Errorf("%w: outbound owned by %s", ErrDirectionBusy, owner)
	}
	for i := len(handlers) - 1; i >= 0; i-- {
		outbound, ok := handlers[i].(OutboundHandler)
		if !ok {
			continue
		}
		action, err := outbound.HandleOutbound(ctx, envelope)
		if err != nil {
			return Envelope{}, fmt.Errorf("middleware %s outbound: %w", handlers[i].Name(), err)
		}
		if action.Consumed {
			return Envelope{}, ErrConsumed
		}
		envelope = action.Envelope
		envelope.Direction = Outbound
	}
	return envelope, nil
}

func (p *Pipeline) Close(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	handlers := append([]Handler(nil), p.handlers...)
	p.handlers = nil
	p.inboundOwner, p.outboundOwner = "", ""
	p.closed = true
	p.mu.Unlock()
	var result error
	for i := len(handlers) - 1; i >= 0; i-- {
		if stopper, ok := handlers[i].(Stopper); ok {
			result = errors.Join(result, stopper.Stop(ctx))
		}
	}
	return result
}
