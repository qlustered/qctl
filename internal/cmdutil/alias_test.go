package cmdutil

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/aliasstore"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestIsRegisteredAlias(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  bool
	}{
		{input: "r-1", want: true},
		{input: "u-1", want: false},
		{input: "v2-1", want: false},
		{input: "orders", want: false},
	} {
		if got := IsRegisteredAlias(tt.input); got != tt.want {
			t.Errorf("IsRegisteredAlias(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestResolveTableRuleAlias(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	const server = "https://api.qluster.test"
	const ruleUUID = "550e8400-e29b-41d4-a716-446655440000"
	serverKey, _ := config.NormalizeEndpointKey(server)

	store, err := aliasstore.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := store.Allocate(serverKey, aliasstore.ResourceDatasetRule, ruleUUID); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	store.Close()

	ctx := &CommandContext{ServerURL: server}

	got, wasAlias, err := ResolveTableRuleAlias(ctx, "r-1")
	if err != nil || !wasAlias || got != ruleUUID {
		t.Fatalf("r-1: got (%q,%v,%v), want (%q,true,nil)", got, wasAlias, err, ruleUUID)
	}

	_, wasAlias, err = ResolveTableRuleAlias(ctx, "email_check")
	if err != nil || wasAlias {
		t.Errorf("email_check: got (wasAlias=%v,err=%v), want (false,nil)", wasAlias, err)
	}

	_, wasAlias, err = ResolveTableRuleAlias(ctx, ruleUUID)
	if err != nil || wasAlias {
		t.Errorf("uuid: got (wasAlias=%v,err=%v), want (false,nil)", wasAlias, err)
	}

	_, wasAlias, err = ResolveTableRuleAlias(ctx, "v2-1")
	if err != nil || wasAlias {
		t.Errorf("v2-1: got (wasAlias=%v,err=%v), want (false,nil)", wasAlias, err)
	}

	_, wasAlias, err = ResolveTableRuleAlias(ctx, "r-99")
	if err == nil {
		t.Errorf("r-99: expected unknown-alias error")
	}
	if wasAlias {
		t.Errorf("r-99: wasAlias should be false on error")
	}
	if !strings.Contains(err.Error(), "r-99") {
		t.Errorf("r-99 error should name the alias, got: %v", err)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("r-99 exit code = %d, want %d", code, qerrors.ExitNotFound)
	}
}

func TestResolveTableRuleAliasMissingStoreIsNotFound(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	_, wasAlias, err := ResolveTableRuleAlias(&CommandContext{ServerURL: "https://api.qluster.test"}, "r-1")
	if wasAlias {
		t.Error("r-1: wasAlias should be false on error")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("r-1 exit code = %d (%v), want %d", code, err, qerrors.ExitNotFound)
	}
}
