package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

// procStatText renders a /proc/stat for a box with the given core count and
// cumulative counters. The guest columns carry a value on purpose: the kernel
// already counts guest time inside user, so a parser that sums every column
// would inflate the denominator and under-report idle.
func procStatText(cores int, idle, total uint64) string {
	var b strings.Builder

	fmt.Fprintf(&b, "cpu  %d 0 0 %d 0 0 0 0 %d 0\n", total-idle, idle, total/4)

	for i := range cores {
		fmt.Fprintf(&b, "cpu%d 0 0 0 0 0 0 0 0 0 0\n", i)
	}

	b.WriteString("intr 12345 0 0\nctxt 6789\nbtime 1757000000\n")

	return b.String()
}

// scriptedProc serves a box whose idle fraction follows idleFractions, one entry
// per sample() call; the last entry repeats forever. An empty pressure string
// means the kernel has no PSI.
func scriptedProc(cores int, pressure string, idleFractions ...float64) readFileFunc {
	const step = 100000

	// The counters are cumulative since boot, so they start non-zero: an
	// all-zero /proc/stat is not a box, it is an unusable sample.
	var idle, total uint64 = 9 * step, 10 * step

	reads := 0

	return func(path string) ([]byte, error) {
		switch path {
		case cpuPressurePath:
			if pressure == "" {
				return nil, fs.ErrNotExist
			}

			return []byte(pressure), nil
		case procStatPath:
			// Reads come in pairs; the closing read of each pair advances the
			// counters by that sample's idle fraction.
			if reads%2 == 1 {
				fraction := idleFractions[min(reads/2, len(idleFractions)-1)]
				idle += uint64(step * fraction)
				total += step
			}

			reads++

			return []byte(procStatText(cores, idle, total)), nil
		}

		return nil, fmt.Errorf("unexpected read of %s", path)
	}
}

// testPreflight wires a preflight to a scripted box, a clock that only moves
// when the preflight sleeps, and a buffer standing in for stderr.
func testPreflight(readFile readFileFunc) (*preflight, *[]time.Duration, *bytes.Buffer) {
	slept := []time.Duration{}
	clock := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	out := &bytes.Buffer{}

	p := &preflight{
		readFile: readFile,
		sleep: func(d time.Duration) {
			slept = append(slept, d)
			clock = clock.Add(d)
		},
		now: func() time.Time { return clock },
		out: out,
	}

	return p, &slept, out
}

const quietPressure = "some avg10=0.30 avg60=0.10 avg300=0.06 total=1246128434\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"

func TestPreflightProceedsOnAQuietBoxAfterOneSample(t *testing.T) {
	p, slept, out := testPreflight(scriptedProc(28, quietPressure, 0.95))

	if err := p.waitForQuietBox(); err != nil {
		t.Fatalf("waitForQuietBox() error = %v, want nil on a quiet box", err)
	}

	if len(*slept) != 1 || (*slept)[0] != sampleInterval {
		t.Errorf("slept = %v, want exactly one %s sample", *slept, sampleInterval)
	}

	if out.Len() != 0 {
		t.Errorf("stderr = %q, want silence on a quiet box", out.String())
	}
}

func TestPreflightWaitsUntilTheBoxClears(t *testing.T) {
	// 26% idle is what a solo `make test` leaves on a 28-core box; the third
	// sample is the box after that run finished.
	p, slept, out := testPreflight(scriptedProc(28, quietPressure, 0.26, 0.26, 0.95))

	if err := p.waitForQuietBox(); err != nil {
		t.Fatalf("waitForQuietBox() error = %v, want nil once the box clears", err)
	}

	if got, want := strings.Count(out.String(), "box busy:"), 2; got != want {
		t.Errorf("progress lines = %d, want %d:\n%s", got, want, out.String())
	}

	if !strings.Contains(out.String(), "7.3 idle cores of 28 (need 9.3)") {
		t.Errorf("progress line does not name the measurement:\n%s", out.String())
	}

	var polls int

	for _, d := range *slept {
		if d == pollInterval {
			polls++
		}
	}

	if polls != 2 {
		t.Errorf("poll sleeps = %d, want 2 (slept = %v)", polls, *slept)
	}
}

func TestPreflightGivesUpAtTheWaitCapNamingTheMeasurement(t *testing.T) {
	p, _, _ := testPreflight(scriptedProc(28, quietPressure, 0.10))

	err := p.waitForQuietBox()
	if err == nil {
		t.Fatal("waitForQuietBox() error = nil, want a refusal on a box that never settles")
	}

	for _, want := range []string{"2.8 idle cores of 28", "need 9.3", "cpu pressure 0.3%", "-no-wait"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestRunPreflightExitsTwoWhenTheBoxNeverSettles(t *testing.T) {
	p, _, _ := testPreflight(scriptedProc(28, quietPressure, 0.10))
	stderr := &bytes.Buffer{}

	if got := runPreflight(p, false, stderr); got != 2 {
		t.Errorf("runPreflight() = %d, want 2", got)
	}

	if !strings.Contains(stderr.String(), "box still busy") {
		t.Errorf("stderr = %q, want the refusal reported", stderr.String())
	}
}

func TestRunPreflightSkipsEntirelyWithNoWait(t *testing.T) {
	p, slept, _ := testPreflight(func(string) ([]byte, error) {
		t.Error("readFile called with -no-wait")

		return nil, fs.ErrNotExist
	})
	stderr := &bytes.Buffer{}

	if got := runPreflight(p, true, stderr); got != 0 {
		t.Errorf("runPreflight() = %d, want 0", got)
	}

	if len(*slept) != 0 {
		t.Errorf("slept = %v, want no delay with -no-wait", *slept)
	}
}

func TestPreflightProceedsWhenProcIsUnreadable(t *testing.T) {
	p, slept, out := testPreflight(func(string) ([]byte, error) { return nil, fs.ErrNotExist })

	if err := p.waitForQuietBox(); err != nil {
		t.Fatalf("waitForQuietBox() error = %v, want nil when /proc cannot be read", err)
	}

	if len(*slept) != 0 || out.Len() != 0 {
		t.Errorf("slept = %v, stderr = %q; want neither on a box it cannot measure", *slept, out.String())
	}
}

func TestPreflightProceedsWhenCountersDoNotAdvance(t *testing.T) {
	frozen := procStatText(28, 500, 1000)
	p, _, _ := testPreflight(func(path string) ([]byte, error) {
		if path == procStatPath {
			return []byte(frozen), nil
		}

		return nil, fs.ErrNotExist
	})

	if err := p.waitForQuietBox(); err != nil {
		t.Fatalf("waitForQuietBox() error = %v, want nil when the counters are frozen", err)
	}
}

func TestPreflightJudgesOnIdleCoresWithoutPSI(t *testing.T) {
	quiet, _, _ := testPreflight(scriptedProc(28, "", 0.95))
	if err := quiet.waitForQuietBox(); err != nil {
		t.Errorf("waitForQuietBox() error = %v, want nil on a quiet box with no PSI", err)
	}

	busy, _, _ := testPreflight(scriptedProc(28, "", 0.10))

	err := busy.waitForQuietBox()
	if err == nil {
		t.Fatal("waitForQuietBox() error = nil, want a refusal on a busy box with no PSI")
	}

	if strings.Contains(err.Error(), "cpu pressure") {
		t.Errorf("error %q reports a pressure it never read", err)
	}
}

func TestPreflightWaitsOnPressureAloneWhenCoresLookFree(t *testing.T) {
	// Plenty of idle cores, but a third of the last ten seconds had a runnable
	// task queueing: utilisation says go, contention says wait.
	queueing := "some avg10=33.00 avg60=20.00 avg300=5.00 total=1\nfull avg10=1.00 avg60=0.00 avg300=0.00 total=0\n"
	p, _, _ := testPreflight(scriptedProc(28, queueing, 0.95))

	err := p.waitForQuietBox()
	if err == nil {
		t.Fatal("waitForQuietBox() error = nil, want a refusal while tasks are queueing")
	}

	if !strings.Contains(err.Error(), "cpu pressure 33.0% (limit 10.0%)") {
		t.Errorf("error %q does not name the pressure that caused it", err)
	}
}

func TestParseCPUStatIgnoresGuestColumns(t *testing.T) {
	// user nice system idle iowait irq softirq steal guest guest_nice. The guest
	// columns are already inside user and nice; counting them again would make
	// total 300 and idle look like 20% of the box instead of 60%.
	stat, err := parseCPUStat([]byte("cpu  40 0 0 60 0 0 0 0 100 0\ncpu0 1 0 0 1 0 0 0 0 0 0\ncpu1 1 0 0 1 0 0 0 0 0 0\nintr 9\n"))
	if err != nil {
		t.Fatalf("parseCPUStat() error = %v", err)
	}

	if stat.total != 100 || stat.idle != 60 || stat.cores != 2 {
		t.Errorf("parseCPUStat() = %+v, want {idle:60 total:100 cores:2}", stat)
	}
}

func TestParseCPUStatCountsIowaitAsIdleAndStealAsBusy(t *testing.T) {
	stat, err := parseCPUStat([]byte("cpu  10 0 0 50 30 0 0 10 0 0\ncpu0 0 0 0 0 0 0 0 0 0 0\n"))
	if err != nil {
		t.Fatalf("parseCPUStat() error = %v", err)
	}

	if stat.idle != 80 || stat.total != 100 {
		t.Errorf("parseCPUStat() = %+v, want idle 80 (idle+iowait) of total 100", stat)
	}
}

func TestParseCPUStatRejectsUnusableInput(t *testing.T) {
	cases := map[string]string{
		"non-numeric counter": "cpu  x 0 0 1 0 0 0 0 0 0\ncpu0 0 0 0 0 0 0 0 0 0 0\n",
		"truncated cpu line":  "cpu  1 2\ncpu0 0 0 0 0 0 0 0 0 0 0\n",
		"no cpu lines":        "intr 1\nctxt 2\n",
		"no per-cpu lines":    "cpu  1 0 0 1 0 0 0 0 0 0\n",
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseCPUStat([]byte(raw)); err == nil {
				t.Errorf("parseCPUStat(%q) error = nil, want a parse failure", raw)
			}
		})
	}
}

func TestParseCPUPressure(t *testing.T) {
	got, err := parseCPUPressure([]byte(quietPressure))
	if err != nil {
		t.Fatalf("parseCPUPressure() error = %v", err)
	}

	if got != 0.30 {
		t.Errorf("parseCPUPressure() = %v, want 0.30", got)
	}

	if _, err := parseCPUPressure([]byte("full avg10=1.00 avg60=0.00\n")); err == nil {
		t.Error("parseCPUPressure() error = nil, want a failure when there is no some line")
	}
}

func TestRequiredIdleCoresHasAFloorAndACap(t *testing.T) {
	cases := []struct {
		cores int
		want  float64
	}{
		{cores: 28, want: 28.0 / 3.0}, // a third of a big box
		{cores: 12, want: 4},
		{cores: 4, want: 2},   // the floor, not 1.33
		{cores: 2, want: 1.8}, // the cap, so a quiet dual-core box can clear it
		{cores: 1, want: 0.9},
	}

	for _, tc := range cases {
		if got := requiredIdleCores(tc.cores); math.Abs(got-tc.want) > 0.001 {
			t.Errorf("requiredIdleCores(%d) = %v, want %v", tc.cores, got, tc.want)
		}
	}
}

// TestParsesThisBoxsProcFiles reads the real files. A fixture can only prove the
// parser handles the shape its author believed in; this catches a kernel that
// prints a different one. It skips wherever the files are absent, which is the
// same path the preflight itself takes.
func TestParsesThisBoxsProcFiles(t *testing.T) {
	raw, err := os.ReadFile(procStatPath)
	if err != nil {
		t.Skipf("no %s on this box: %v", procStatPath, err)
	}

	stat, err := parseCPUStat(raw)
	if err != nil {
		t.Fatalf("parseCPUStat(%s) error = %v", procStatPath, err)
	}

	if stat.cores == 0 || stat.idle == 0 || stat.idle > stat.total {
		t.Errorf("parseCPUStat(%s) = %+v, want a plausible box", procStatPath, stat)
	}

	raw, err = os.ReadFile(cpuPressurePath)
	if err != nil {
		t.Skipf("no %s on this box (PSI not compiled in): %v", cpuPressurePath, err)
	}

	pressure, err := parseCPUPressure(raw)
	if err != nil {
		t.Fatalf("parseCPUPressure(%s) error = %v", cpuPressurePath, err)
	}

	if pressure < 0 || pressure > 100 {
		t.Errorf("parseCPUPressure(%s) = %v, want a percentage", cpuPressurePath, pressure)
	}
}

func TestPreflightClearsAQuietSmallBox(t *testing.T) {
	// A dual-core machine can never offer the 2-core floor, so requiredIdleCores
	// caps the demand at 90% of the box. Without that cap this box would wait
	// out the full three minutes and then refuse, while completely idle.
	p, _, out := testPreflight(scriptedProc(2, quietPressure, 0.97))

	if err := p.waitForQuietBox(); err != nil {
		t.Fatalf("waitForQuietBox() error = %v, want nil on a quiet 2-core box", err)
	}

	if out.Len() != 0 {
		t.Errorf("stderr = %q, want silence", out.String())
	}
}
