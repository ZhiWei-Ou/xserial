package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/capture"
	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
	"github.com/ZhiWei-Ou/xserial/internal/rawui"
	"github.com/ZhiWei-Ou/xserial/internal/replay"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newReplayCommand() *cobra.Command {
	var frameRule string
	var hexdump bool
	cmd := &cobra.Command{Use: "replay <capture.xsr>", Short: "Replay a recorded session offline; never opens a serial port", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("frame") && !hexdump {
				return errors.New("--frame requires --hexdump for replay")
			}
			framing, err := replay.ParseFrameConfig(frameRule)
			if err != nil {
				return err
			}
			session, err := capture.Load(args[0])
			if err != nil {
				return err
			}
			cfg := replay.Config{Input: cmd.InOrStdin(), Output: cmd.OutOrStdout(), Local: cmd.ErrOrStderr(), Hexdump: hexdump, Framing: framing}
			input, inputFile := cfg.Input.(*os.File)
			output, outputFile := cfg.Output.(*os.File)
			if inputFile && outputFile && term.IsTerminal(int(input.Fd())) && term.IsTerminal(int(output.Fd())) {
				cfg.Terminal = rawui.NewOSTerminal(input)
				cfg.Size = func() (int, int) {
					width, height, err := term.GetSize(int(output.Fd()))
					if err != nil || width < 1 || height < 1 {
						return 100, 28
					}
					return width, height
				}
			}
			return replay.Run(cmd.Context(), cfg, session)
		}}
	cmd.Flags().BoolVar(&hexdump, "hexdump", false, "display recorded TX/RX as HEX instead of native terminal output")
	cmd.Flags().StringVar(&frameRule, "frame", "newline", "HEX frame rule: newline, gap:DURATION, delimiter:HEX, fixed:N")
	return cmd
}

func newExportCommand() *cobra.Command {
	var output, format, match string
	var from, to time.Duration
	cmd := &cobra.Command{Use: "export <capture.xsr>", Short: "Export capture events as text or a filtered .xsr recording", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) (runErr error) {
			if from < 0 || to < 0 || (to > 0 && to < from) {
				return errors.New("require 0 <= from <= to; to=0 means no upper bound")
			}
			if format != "text" && format != "jsonl" {
				return fmt.Errorf("unknown export format %q", format)
			}
			pattern, err := hexdata.Parse(match)
			if err != nil {
				return err
			}
			session, err := capture.Load(args[0])
			if err != nil {
				return err
			}
			var dst io.Writer = cmd.OutOrStdout()
			marks, err := capture.LoadMarks(args[0] + ".marks.json")
			if err != nil {
				return fmt.Errorf("load capture marks: %w", err)
			}
			seq := uint64(0)
			if len(session.Records) > 0 {
				seq = session.Records[len(session.Records)-1].Seq
			}
			for _, mark := range marks {
				seq++
				session.Records = append(session.Records, capture.Record{Seq: seq, At: mark.At, Kind: "mark", Note: mark.Note})
			}
			if output != "" {
				file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
				if err != nil {
					return fmt.Errorf("create export: %w", err)
				}
				defer func() { runErr = errors.Join(runErr, file.Close()) }()
				dst = file
			}
			return capture.Export(dst, session, format, capture.Filter{From: from, To: to, Match: pattern})
		}}
	cmd.Flags().StringVarP(&output, "output", "o", "", "new output file (default: stdout)")
	cmd.Flags().StringVar(&format, "format", "text", "text or jsonl")
	cmd.Flags().StringVar(&match, "match", "", "include records containing these hex bytes")
	cmd.Flags().DurationVar(&from, "from", 0, "start offset from recording start, e.g. 2s")
	cmd.Flags().DurationVar(&to, "to", 0, "end offset; zero means no upper bound")
	return cmd
}
