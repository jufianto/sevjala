package svc

import (
	"reflect"
	"testing"
)

func TestGroupCommands(t *testing.T) {
	tests := []struct {
		goos   string
		exists bool
		user   string
		want   [][]string
	}{
		{"linux", false, "jufi", [][]string{{"groupadd", "--system", "sevjala"}, {"usermod", "-aG", "sevjala", "jufi"}}},
		{"linux", true, "", nil},
		{"darwin", false, "", [][]string{{"dseditgroup", "-o", "create", "-r", "Sevjala", "sevjala"}}},
		{"darwin", true, "jufi", [][]string{{"dseditgroup", "-o", "edit", "-a", "jufi", "-t", "user", "sevjala"}}},
		{"windows", false, "jufi", nil},
	}
	for _, tt := range tests {
		if got := groupCommands(tt.goos, tt.exists, tt.user); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("groupCommands(%s, %v, %q) = %v, want %v", tt.goos, tt.exists, tt.user, got, tt.want)
		}
	}
}
