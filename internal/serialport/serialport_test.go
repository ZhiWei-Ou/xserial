package serialport

import "testing"

func TestFormatInfo(t *testing.T) {
	tests := []struct {
		name string
		info Info
		want string
	}{
		{
			name: "plain port",
			info: Info{Name: "/dev/cu.debug"},
			want: "/dev/cu.debug",
		},
		{
			name: "usb port",
			info: Info{
				Name:         "/dev/cu.usbserial",
				IsUSB:        true,
				VID:          "0403",
				PID:          "6001",
				SerialNumber: "A50285BI",
				Product:      "USB Serial",
			},
			want: "/dev/cu.usbserial vid=0403 pid=6001 serial=A50285BI product=USB Serial",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatInfo(tt.info)
			if got != tt.want {
				t.Fatalf("FormatInfo() = %q, want %q", got, tt.want)
			}
		})
	}
}
