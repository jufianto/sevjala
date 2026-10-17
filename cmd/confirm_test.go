package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jufianto/sevjala/localapi"
)

func TestWithConfirm(t *testing.T) {
	need := localapi.ConfirmRequired("this change to policy daily deletes 2 backups on PC-1", "daily", 2, []string{"PC-1"})
	tests := []struct {
		name, given, typed string
		answers            []error // what each call returns
		calls              []string
		err                error
		errHas             string
		out                string
	}{
		{"no confirmation needed", "", "", []error{nil}, []string{""}, nil, "", ""},
		{"typed the word", "", "daily\n", []error{need, nil}, []string{"", "daily"}, nil, "", "Type daily to continue"},
		{"typed something else", "", "dialy\n", []error{need}, []string{""}, errSilent1, "", "Cancelled: nothing was changed."},
		{"given and still refused", "x", "", []error{need}, []string{"x"}, need, `--confirm "x" is not the word: this change to policy daily deletes 2 backups on PC-1; to go ahead use --confirm daily`, ""},
		{"given and right", "daily", "", []error{nil}, []string{"daily"}, nil, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			var out bytes.Buffer
			_, err := withConfirm(strings.NewReader(tt.typed), &out, tt.given, func(confirm string) (int, error) {
				calls = append(calls, confirm)
				return 0, tt.answers[len(calls)-1]
			})
			if !errors.Is(err, tt.err) || tt.errHas != "" && !strings.Contains(err.Error(), tt.errHas) {
				t.Errorf("err %v, want %v", err, tt.err)
			}
			if strings.Join(calls, "|") != strings.Join(tt.calls, "|") {
				t.Errorf("calls %q", calls)
			}
			if !strings.Contains(out.String(), tt.out) {
				t.Errorf("out %q", out.String())
			}
		})
	}
}
