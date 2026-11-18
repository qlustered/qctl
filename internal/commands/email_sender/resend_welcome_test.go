package email_sender //nolint:revive // Package name mirrors the command.

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

const resendBobPath = rmSendersPath + "/0190eeee-0000-4000-8000-000000000002/welcome/resend"

func TestResendWelcomeUsageErrorsMakeNoRequest(t *testing.T) {
	for name, args := range rmUsageCases {
		t.Run(name, func(t *testing.T) {
			mock := rmTestEnv(t)
			_, _, err := rmRunCmd(t, append([]string{"resend-welcome"}, args...)...)
			rmAssertExit(t, err, qerrors.ExitBadUsage)
			rmAssertRequests(t, mock)
		})
	}
}

func TestResendWelcomeResolvesThenSchedulesWithoutAPrompt(t *testing.T) {
	mock := rmTestEnv(t)
	rmServe(mock, http.MethodGet, rmSendersPath, http.StatusOK, rmSendersBody)
	rmServe(mock, http.MethodPost, resendBobPath, http.StatusOK, []byte(`{"outbound_email_id":7731}`))

	out, _, err := rmRunCmd(t, "resend-welcome", "BOB@example.com", "--cloud-source-id", "42")
	if err != nil {
		t.Fatalf("resend-welcome: %v", err)
	}
	rmAssertRequests(t, mock, "GET "+rmSendersPath, "POST "+resendBobPath)
	for _, want := range []string{"bob@example.com", "cloud-source/42", "7731"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q lacks %q", out, want)
		}
	}
}

func TestResendWelcomeMachineOutputs(t *testing.T) {
	cases := map[string]func(t *testing.T, out string){
		"json": func(t *testing.T, out string) {
			var got map[string]any
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("output is not JSON: %v\n%s", err, out)
			}
			if want := map[string]any{"outbound_email_id": float64(7731)}; !reflect.DeepEqual(got, want) {
				t.Errorf("output = %v, want %v", got, want)
			}
		},
		"yaml": func(t *testing.T, out string) {
			if out != "outbound_email_id: 7731\n" {
				t.Errorf("output = %q", out)
			}
		},
		"name": func(t *testing.T, out string) {
			if out != "email-sender/"+rmBobID.String()+"\n" {
				t.Errorf("output = %q", out)
			}
		},
	}
	for format, check := range cases {
		t.Run(format, func(t *testing.T) {
			mock := rmTestEnv(t)
			rmServe(mock, http.MethodGet, rmSendersPath, http.StatusOK, rmSendersBody)
			rmServe(mock, http.MethodPost, resendBobPath, http.StatusOK, []byte(`{"outbound_email_id":7731}`))
			out, _, err := rmRunCmd(t, "resend-welcome", "email-sender/"+rmBobID.String(), "--cloud-source-id", "42", "-o", format)
			if err != nil {
				t.Fatalf("resend-welcome: %v", err)
			}
			check(t, out)
		})
	}
}

func TestResendWelcomeUnassignedSenderExitsThreeWithoutSending(t *testing.T) {
	mock := rmTestEnv(t)
	rmServe(mock, http.MethodGet, rmSendersPath, http.StatusOK, rmSendersBody)
	_, _, err := rmRunCmd(t, "resend-welcome", "carol@example.com", "--cloud-source-id", "42")
	rmAssertExit(t, err, qerrors.ExitNotFound)
	rmAssertRequests(t, mock, "GET "+rmSendersPath)
}

func TestResendWelcomeInactiveSourceExitsTwo(t *testing.T) {
	mock := rmTestEnv(t)
	rmServe(mock, http.MethodGet, rmSendersPath, http.StatusOK, rmSendersBody)
	rmServe(mock, http.MethodPost, resendBobPath, http.StatusConflict,
		[]byte(`{"detail":{"msg":"Activate the intake before sending a welcome."}}`))
	_, _, err := rmRunCmd(t, "resend-welcome", rmBobID.String(), "--cloud-source-id", "42")
	rmAssertExit(t, err, qerrors.ExitBadUsage)
}
