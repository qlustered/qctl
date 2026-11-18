package main

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// CPU preflight. A test run that starts on a box which is already saturated
// does not fail honestly: it starves the handful of deadline-shaped tests
// (internal/ws, internal/commands/get/job_activity, internal/auth all sleep for
// tens of milliseconds against multi-second deadlines) and the failure reads as
// a bug in the code under test. CLAUDE.md used to ask every human and agent to
// eyeball `uptime` against `nproc` first; nothing enforced it, and the load
// average is the wrong primary signal anyway -- it decays over ~60s, so a burst
// that started ten seconds ago barely registers, and it counts tasks blocked on
// disk as well as on CPU.
//
// Two plain files replace that eyeball check:
//
//	/proc/stat          sampled twice, ~1s apart -> how many cores are idle NOW
//	/proc/pressure/cpu  "some avg10" -> the share of the last 10s in which a
//	                    runnable task was WAITING for a CPU
//
// Utilisation alone would not do: a box can be fully busy with nothing queueing
// and every test still gets its slice. Contention is what hurts, and PSI is the
// only one of the three that measures it directly.
//
// Everything here degrades to "proceed". A preflight that cannot measure the
// box must not stand between a developer and their tests.
const (
	procStatPath    = "/proc/stat"
	cpuPressurePath = "/proc/pressure/cpu"

	// sampleInterval separates the two /proc/stat reads. The counters are
	// cumulative jiffies, so a delta needs elapsed time; one second moves them
	// on every tick rate in use and is the entire cost the preflight imposes on
	// a quiet box.
	sampleInterval = time.Second

	// pollInterval is the pause before re-sampling a busy box. Each poll pays
	// sampleInterval on top of it, so a cycle is about four seconds.
	pollInterval = 3 * time.Second

	// waitCap bounds the whole preflight. It has to leave room for the run
	// itself inside whatever timeout the caller set -- an agent's Bash tool caps
	// at ten minutes and a full run of this suite takes about one.
	waitCap = 3 * time.Minute

	// minIdleCoreFraction is the share of the box that must be idle before a run
	// starts. Measured 2026-09-10 on the 28-core dev box: idle sat at ~26.5
	// cores (95%) while quiet and bottomed at 7.2 cores (26%) during a solo
	// `make test`. A third of the box therefore clears a quiet machine with room
	// to spare while refusing to stack a second suite on top of a first, which
	// CLAUDE.md asks for and nothing used to enforce.
	minIdleCoreFraction = 1.0 / 3.0

	// minIdleCores is the floor for small machines, where a third of the box
	// rounds down to almost nothing.
	minIdleCores = 2.0

	// maxIdleCoreDemand caps that floor as a share of the box, so a completely
	// quiet single- or dual-core machine still clears a bar it could never
	// otherwise reach.
	maxIdleCoreDemand = 0.9

	// maxCPUPressure caps /proc/pressure/cpu "some avg10", in percent. A solo
	// `make test` peaked at 3.95% on the box above, so a legitimate back-to-back
	// re-run is not blocked by its predecessor's decay, while sustained double
	// digits means work is already queueing for a CPU.
	maxCPUPressure = 10.0
)

// readFileFunc reads one /proc file. It is injected so the preflight's tests
// describe a box instead of depending on the one they run on.
type readFileFunc func(path string) ([]byte, error)

type preflight struct {
	readFile readFileFunc
	sleep    func(time.Duration)
	now      func() time.Time
	out      io.Writer
}

func newPreflight() *preflight {
	return &preflight{
		readFile: os.ReadFile,
		sleep:    time.Sleep,
		now:      time.Now,
		out:      os.Stderr,
	}
}

// cpuStat is the pair of cumulative counters an idle fraction is computed from,
// plus the core count the per-CPU lines imply.
type cpuStat struct {
	idle  uint64
	total uint64
	cores int
}

// cpuSnapshot is one measurement of the box. Pressure is optional: a kernel
// built without PSI has no /proc/pressure/cpu, and the idle-core check still
// stands on its own.
type cpuSnapshot struct {
	idleCores    float64
	cores        int
	pressure     float64
	havePressure bool
}

func (c cpuSnapshot) busy() bool {
	if c.idleCores < requiredIdleCores(c.cores) {
		return true
	}

	return c.havePressure && c.pressure > maxCPUPressure
}

func (c cpuSnapshot) String() string {
	s := fmt.Sprintf("%.1f idle cores of %d (need %.1f)", c.idleCores, c.cores, requiredIdleCores(c.cores))
	if c.havePressure {
		s += fmt.Sprintf(", cpu pressure %.1f%% (limit %.1f%%)", c.pressure, maxCPUPressure)
	}

	return s
}

// requiredIdleCores is the idle-core bar for a box of this size.
func requiredIdleCores(cores int) float64 {
	need := math.Max(float64(cores)*minIdleCoreFraction, minIdleCores)

	return math.Min(need, float64(cores)*maxIdleCoreDemand)
}

// waitForQuietBox blocks until the box has room for a test run, and returns an
// error only when the wait cap runs out. Every other outcome -- no /proc, a
// kernel without PSI, counters that will not advance -- means the preflight
// cannot judge, and it proceeds rather than blocking a run on its own blindness.
func (p *preflight) waitForQuietBox() error {
	deadline := p.now().Add(waitCap)

	for {
		snapshot, err := p.sample()
		if err != nil || !snapshot.busy() {
			return nil
		}

		if !p.now().Before(deadline) {
			return fmt.Errorf(
				"box still busy after %s: %s; wait for it to settle, or skip this check with `make test TEST_ARGS=-no-wait`",
				waitCap, snapshot,
			)
		}

		// One line per poll, so a watching human or agent sees waiting rather
		// than a hang.
		fmt.Fprintf(p.out, "box busy: %s -- waiting\n", snapshot)
		p.sleep(pollInterval)
	}
}

// sample reads /proc/stat twice, sampleInterval apart, and pairs the resulting
// idle fraction with the current CPU pressure.
func (p *preflight) sample() (cpuSnapshot, error) {
	first, err := p.readCPUStat()
	if err != nil {
		return cpuSnapshot{}, err
	}

	p.sleep(sampleInterval)

	second, err := p.readCPUStat()
	if err != nil {
		return cpuSnapshot{}, err
	}

	if second.total <= first.total || second.idle < first.idle {
		return cpuSnapshot{}, errors.New(procStatPath + " counters did not advance")
	}

	idleFraction := float64(second.idle-first.idle) / float64(second.total-first.total)
	snapshot := cpuSnapshot{
		cores:     second.cores,
		idleCores: float64(second.cores) * idleFraction,
	}

	if pressure, err := p.readCPUPressure(); err == nil {
		snapshot.pressure, snapshot.havePressure = pressure, true
	}

	return snapshot, nil
}

func (p *preflight) readCPUStat() (cpuStat, error) {
	raw, err := p.readFile(procStatPath)
	if err != nil {
		return cpuStat{}, err
	}

	return parseCPUStat(raw)
}

func (p *preflight) readCPUPressure() (float64, error) {
	raw, err := p.readFile(cpuPressurePath)
	if err != nil {
		return 0, err
	}

	return parseCPUPressure(raw)
}

// parseCPUStat reads the aggregate `cpu` line for the counters and counts the
// per-CPU `cpuN` lines for the core count, so both come from the same sample.
func parseCPUStat(raw []byte) (cpuStat, error) {
	var stat cpuStat

	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "cpu") {
			continue
		}

		if fields[0] != "cpu" {
			stat.cores++

			continue
		}

		if err := sumCPUFields(&stat, fields); err != nil {
			return cpuStat{}, err
		}
	}

	if stat.total == 0 || stat.cores == 0 {
		return cpuStat{}, errors.New("no usable cpu lines in " + procStatPath)
	}

	return stat, nil
}

// sumCPUFields totals the aggregate line. The fields are user, nice, system,
// idle, iowait, irq, softirq, steal, guest, guest_nice; only the first eight are
// summed, because the kernel already counts guest time inside user and nice and
// adding it again would inflate the denominator. idle+iowait is time no runnable
// task used the CPU; steal is time a hypervisor took, which is not capacity this
// box can offer.
func sumCPUFields(stat *cpuStat, fields []string) error {
	const (
		maxCounters = 8
		idleIndex   = 3
		iowaitIndex = 4
	)

	counters := fields[1:]
	if len(counters) < idleIndex+1 {
		return fmt.Errorf("malformed cpu line in %s: %q", procStatPath, strings.Join(fields, " "))
	}

	if len(counters) > maxCounters {
		counters = counters[:maxCounters]
	}

	for i, field := range counters {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return fmt.Errorf("malformed cpu line in %s: %q", procStatPath, strings.Join(fields, " "))
		}

		stat.total += value
		if i == idleIndex || i == iowaitIndex {
			stat.idle += value
		}
	}

	return nil
}

// parseCPUPressure pulls "some avg10" out of /proc/pressure/cpu -- the share of
// the last ten seconds in which at least one runnable task waited for a CPU.
func parseCPUPressure(raw []byte) (float64, error) {
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "some" {
			continue
		}

		for _, field := range fields[1:] {
			key, value, ok := strings.Cut(field, "=")
			if !ok || key != "avg10" {
				continue
			}

			return strconv.ParseFloat(value, 64)
		}
	}

	return 0, errors.New(`no "some avg10" line in ` + cpuPressurePath)
}

// runPreflight reports the exit code the preflight demands: 0 to carry on, 2
// when the box stayed busy past the wait cap. A box that never settles is an
// environment refusal rather than a test failure, so it takes the usage-error
// exit code and not go test's 1.
func runPreflight(p *preflight, skip bool, stderr io.Writer) int {
	if skip {
		return 0
	}

	if err := p.waitForQuietBox(); err != nil {
		fmt.Fprintf(stderr, "%v\n", err)

		return 2
	}

	return 0
}
