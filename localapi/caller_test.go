package localapi

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestAdminRequiredViaManager(t *testing.T) {
	via := WithVia(context.Background(), Via{Manager: "BACKUP-PC-1", Controller: "SERVER-01"})
	tests := []struct {
		name         string
		ctx          context.Context
		msg, reason  string
		elevatesHere bool
	}{
		{"at the controller", context.Background(), "restoring from a file needs administrator rights", reasonAdminRequired, true},
		{"from a manager", via, "restoring from a file needs administrator rights on SERVER-01 itself", "controller_admin_required", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := AdminRequired(tt.ctx, "restoring from a file")
			var e *Error
			if !errors.As(err, &e) || e.Code != http.StatusForbidden || e.Msg != tt.msg || e.Reason != tt.reason {
				t.Errorf("got %#v", err)
			}
			if IsAdminRequired(err) != tt.elevatesHere {
				t.Errorf("IsAdminRequired %v", IsAdminRequired(err))
			}
		})
	}
	if v, ok := ViaOf(via); !ok || v.Manager != "BACKUP-PC-1" {
		t.Errorf("ViaOf %+v %v", v, ok)
	}
	if _, ok := ViaOf(context.Background()); ok {
		t.Error("ViaOf on a plain context")
	}
}
