package middleware

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type testHandler struct {
	name              string
	capability        Capability
	inbound, outbound func(Envelope) (Action, error)
}

func (h testHandler) Name() string           { return h.name }
func (h testHandler) Capability() Capability { return h.capability }
func (h testHandler) HandleInbound(_ context.Context, e Envelope) (Action, error) {
	if h.inbound == nil {
		return Forward(e), nil
	}
	return h.inbound(e)
}
func (h testHandler) HandleOutbound(_ context.Context, e Envelope) (Action, error) {
	if h.outbound == nil {
		return Forward(e), nil
	}
	return h.outbound(e)
}

func TestPipelineUsesOppositeDirectionOrder(t *testing.T) {
	var got []string
	h := func(name string) testHandler {
		return testHandler{name: name, inbound: func(e Envelope) (Action, error) { got = append(got, name+"i"); return Forward(e), nil }, outbound: func(e Envelope) (Action, error) { got = append(got, name+"o"); return Forward(e), nil }}
	}
	p, err := NewPipeline(h("a"), h("b"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.ProcessInbound(context.Background(), Envelope{Data: []byte{1}}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ProcessOutbound(context.Background(), Envelope{Data: []byte{2}}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"ai", "bi", "bo", "ao"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order=%v want=%v", got, want)
	}
}

func TestExclusiveHandlerRejectsOtherOutboundSources(t *testing.T) {
	p, _ := NewPipeline()
	h := testHandler{name: "upload", capability: ExclusiveOutbound}
	if err := p.Add(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ProcessOutbound(context.Background(), Envelope{Source: "frontend", Data: []byte{1}}); !errors.Is(err, ErrDirectionBusy) {
		t.Fatalf("error=%v", err)
	}
	if _, err := p.ProcessOutbound(context.Background(), Envelope{Source: "upload", Data: []byte{1}}); err != nil {
		t.Fatal(err)
	}
	if err := p.Remove(context.Background(), "upload"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ProcessOutbound(context.Background(), Envelope{Source: "frontend", Data: []byte{1}}); err != nil {
		t.Fatal(err)
	}
}

func TestConsumedEnvelopeStopsPipeline(t *testing.T) {
	called := false
	p, _ := NewPipeline(testHandler{name: "consume", inbound: func(Envelope) (Action, error) { return Consume(), nil }}, testHandler{name: "later", inbound: func(e Envelope) (Action, error) { called = true; return Forward(e), nil }})
	if _, err := p.ProcessInbound(context.Background(), Envelope{Data: []byte{1}}); !errors.Is(err, ErrConsumed) {
		t.Fatalf("error=%v", err)
	}
	if called {
		t.Fatal("later handler was called")
	}
}

func TestClosedPipelineRejectsProcessingAndMutation(t *testing.T) {
	p, err := NewPipeline(testHandler{name: "observer"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ProcessInbound(context.Background(), Envelope{Data: []byte{1}}); !errors.Is(err, ErrPipelineClosed) {
		t.Fatalf("ProcessInbound() error = %v", err)
	}
	if err := p.Add(context.Background(), testHandler{name: "late"}); !errors.Is(err, ErrPipelineClosed) {
		t.Fatalf("Add() error = %v", err)
	}
	if err := p.Remove(context.Background(), "observer"); !errors.Is(err, ErrPipelineClosed) {
		t.Fatalf("Remove() error = %v", err)
	}
}
