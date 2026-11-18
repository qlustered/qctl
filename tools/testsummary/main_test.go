package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestProcessTestOutput(t *testing.T) {
	input := strings.NewReader(
		"{\"Action\":\"output\",\"Package\":\"example/one\",\"Test\":\"TestOne\",\"Output\":\"--- FAIL: TestOne\\n\"}\n" +
			"{\"Action\":\"fail\",\"Package\":\"example/one\",\"Test\":\"TestOne\"}\n" +
			"{\"Action\":\"fail\",\"Package\":\"example/two\"}\n",
	)
	var output bytes.Buffer

	failedTests, failedPackages, err := processTestOutput(input, &output)
	if err != nil {
		t.Fatalf("processTestOutput() error = %v", err)
	}
	if got, want := output.String(), "--- FAIL: TestOne\n"; got != want {
		t.Errorf("forwarded output = %q, want %q", got, want)
	}
	if _, ok := failedTests["example/one"]["TestOne"]; !ok {
		t.Errorf("failed tests = %#v, want example/one TestOne", failedTests)
	}
	if _, ok := failedPackages["example/two"]; !ok {
		t.Errorf("failed packages = %#v, want example/two", failedPackages)
	}
}

func TestProcessTestOutputRejectsMalformedJSON(t *testing.T) {
	_, _, err := processTestOutput(strings.NewReader("not JSON\n"), &bytes.Buffer{})
	if err == nil {
		t.Fatal("processTestOutput() error = nil, want JSON decode error")
	}
}
