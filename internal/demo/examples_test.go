package demo

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/ZhiWei-Ou/xserial/internal/capture"
	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
)

func TestPublishedExamplesContainValidQueriesAndReplayableCapture(t *testing.T) {
	data, err := os.ReadFile("../../examples/modbus/commands.json")
	if err != nil {
		t.Fatal(err)
	}
	var commands []struct{ Name, Hex string }
	if err := json.Unmarshal(data, &commands); err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		frame, err := hexdata.Parse(command.Hex)
		if err != nil {
			t.Fatal(err)
		}
		valid, err := hexdata.VerifyChecksum(frame, "crc16-modbus")
		if err != nil || !valid {
			t.Fatalf("example %q has invalid CRC: %s", command.Name, command.Hex)
		}
	}
	session, err := capture.Load("../../examples/modbus/demo.xsr")
	if err != nil || len(session.Records) == 0 {
		t.Fatalf("example capture: %v", err)
	}
}
