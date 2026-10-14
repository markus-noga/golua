//go:build !noquotas
// +build !noquotas

package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/arnodel/golua/runtime/internal/luagc"
	"github.com/arnodel/golua/safeio"
)

const QuotasAvailable = true

// When tracking time, the runtimeContextManager will take the opportunity to
// update the time spent in the context when the CPU is increased.  It doesn't
// do that every time because it would slow down the runtime too much for little
// benefit.  This value sets the amount of CPU required that triggers a time
// update.
const cpuThresholdIncrement = 10000

type runtimeContextManager struct {
	hardLimits    RuntimeResources
	softLimits    RuntimeResources
	usedResources RuntimeResources

	requiredFlags ComplianceFlags

	status RuntimeContextStatus

	parent *runtimeContextManager

	messageHandler Callable

	trackCpu         bool
	fuel, granted    int64 // CPU ticks left of those granted by the last grantFuel, and the number granted: see RequireCPU
	trackMem         bool
	trackTime        bool
	stopLevel        StopLevel
	startTime        uint64
	nextCpuThreshold uint64

	weakRefPool luagc.Pool
	poolFactory func() luagc.Pool
	gcPolicy    GCPolicy

	fsAccess *safeio.FSAccess

	cache *ctxCache // shared by every copy of the manager, see PushContext
}

// ctxCache holds what PushContext and PopContext would otherwise allocate on
// every call: the heap copies of the parent context and the weak pools of
// contexts with limits.  Only the Fast variants (CallContextFast) recycle them,
// because the RuntimeContext returned by PopContext keeps pointing at its
// parent.
type ctxCache struct {
	parents []*runtimeContextManager
	pools   []luagc.Pool
}

func (c *ctxCache) getParent() *runtimeContextManager {
	if n := len(c.parents); n > 0 {
		p := c.parents[n-1]
		c.parents = c.parents[:n-1]
		return p
	}
	return new(runtimeContextManager)
}

var _ RuntimeContext = (*runtimeContextManager)(nil)

func (m *runtimeContextManager) initRoot() {
	m.gcPolicy = IsolateGCPolicy
	m.weakRefPool = m.poolFactory()
	m.fsAccess = safeio.Unrestricted
	m.cache = new(ctxCache)
}

func (m *runtimeContextManager) HardLimits() RuntimeResources {
	return m.hardLimits
}

func (m *runtimeContextManager) SoftLimits() RuntimeResources {
	return m.softLimits
}

func (m *runtimeContextManager) UsedResources() RuntimeResources {
	m.syncCPU()
	return m.usedResources
}

func (m *runtimeContextManager) Status() RuntimeContextStatus {
	return m.status
}

func (m *runtimeContextManager) setStatus(st RuntimeContextStatus) {
	m.status = st
}

func (m *runtimeContextManager) RequiredFlags() ComplianceFlags {
	return m.requiredFlags
}

func (m *runtimeContextManager) CheckRequiredFlags(flags ComplianceFlags) error {
	missingFlags := m.requiredFlags &^ flags
	if missingFlags != 0 {
		return fmt.Errorf("missing flags: %s", strings.Join(missingFlags.Names(), " "))
	}
	return nil
}

func (m *runtimeContextManager) Parent() RuntimeContext {
	return m.parent
}

func (m *runtimeContextManager) SetStopLevel(stopLevel StopLevel) {
	m.stopLevel |= stopLevel
	if stopLevel&HardStop != 0 && m.status == StatusLive {
		m.KillContext()
	}
}

func (m *runtimeContextManager) Due() bool {
	m.syncCPU()
	return m.stopLevel&SoftStop != 0 || !m.softLimits.Dominates(m.usedResources)
}

func (m *runtimeContextManager) RuntimeContext() RuntimeContext {
	return m
}

func (m *runtimeContextManager) PushContext(ctx RuntimeContextDef) {
	if m.trackTime {
		m.updateTimeUsed()
	}
	m.syncCPU()
	parent := m.cache.getParent()
	*parent = *m
	m.hardLimits = m.hardLimits.Remove(m.usedResources).Merge(ctx.HardLimits)
	m.softLimits = m.hardLimits.Merge(m.softLimits).Merge(ctx.SoftLimits)
	m.usedResources = RuntimeResources{}
	m.requiredFlags |= ctx.RequiredFlags

	if ctx.HardLimits.Cpu > 0 {
		m.requiredFlags |= ComplyCpuSafe
	}
	if ctx.HardLimits.Memory > 0 {
		m.requiredFlags |= ComplyMemSafe
	}
	if ctx.HardLimits.Millis > 0 {
		m.requiredFlags |= ComplyTimeSafe
	}
	if ctx.FSAccess != nil {
		if ctx.FSAccess.Parent() != m.fsAccess {
			panic("RuntimeContextDef.FSAccess must be derived from the current context's FSAccess")
		}
		m.fsAccess = ctx.FSAccess
		m.requiredFlags |= ComplyIoSafe
	} else if m.requiredFlags&ComplyIoSafe != 0 && m.fsAccess.IsUnrestricted() {
		m.fsAccess = safeio.NoAccess
	}
	m.trackTime = m.hardLimits.Millis > 0 || m.softLimits.Millis > 0
	if m.trackTime { // only a context with a time limit reads the time it started at: a call of the clock for each call of a rule is 2% of a run
		m.startTime = now()
	}
	m.trackCpu = m.hardLimits.Cpu > 0 || m.softLimits.Cpu > 0 || m.trackTime
	m.trackMem = m.hardLimits.Memory > 0 || m.softLimits.Memory > 0
	m.grantFuel()
	m.status = StatusLive
	m.messageHandler = ctx.MessageHandler
	m.parent = parent
	if ctx.GCPolicy == IsolateGCPolicy || ctx.HardLimits.Millis > 0 || ctx.HardLimits.Cpu > 0 || ctx.HardLimits.Memory > 0 {
		if n := len(m.cache.pools); n > 0 {
			m.weakRefPool = m.cache.pools[n-1]
			m.cache.pools = m.cache.pools[:n-1]
		} else {
			m.weakRefPool = m.poolFactory()
		}
		m.gcPolicy = IsolateGCPolicy
	} else {
		m.weakRefPool = parent.weakRefPool
		m.gcPolicy = ShareGCPolicy
	}
}

func (m *runtimeContextManager) GCPolicy() GCPolicy {
	return m.gcPolicy
}

func (m *runtimeContextManager) PopContext() RuntimeContext {
	if m == nil || m.parent == nil {
		return nil
	}
	m.syncCPU()
	if m.gcPolicy == IsolateGCPolicy {
		m.weakRefPool.ExtractAllMarkedFinalize()
		releaseResources(m.weakRefPool.ExtractAllMarkedRelease())
	}
	if m.fsAccess != m.parent.fsAccess {
		_ = m.fsAccess.Close()
	}
	mCopy := *m
	if mCopy.status == StatusLive {
		mCopy.status = StatusDone
	}
	m.parent.RequireCPU(m.usedResources.Cpu)
	m.parent.RequireMem(m.usedResources.Memory)
	*m = *m.parent
	if m.trackTime {
		m.updateTimeUsed()
	}
	return &mCopy
}

// popContextFast is PopContext without the copy of the context for the
// caller; the parent copy and the weak pool go back to the cache.
func (m *runtimeContextManager) popContextFast() {
	parent := m.parent
	if parent == nil {
		return
	}
	m.syncCPU()
	pool := m.weakRefPool
	isolated := m.gcPolicy == IsolateGCPolicy
	if isolated {
		pool.ExtractAllMarkedFinalize()
		releaseResources(pool.ExtractAllMarkedRelease())
	}
	if m.fsAccess != parent.fsAccess {
		_ = m.fsAccess.Close()
	}
	used := m.usedResources
	parent.RequireCPU(used.Cpu)
	parent.RequireMem(used.Memory)
	*m = *parent
	if isolated {
		m.cache.pools = append(m.cache.pools, pool)
	}
	*parent = runtimeContextManager{}
	m.cache.parents = append(m.cache.parents, parent)
	if m.trackTime {
		m.updateTimeUsed()
	}
}

// cpuFuelChunk is how many CPU ticks at most are granted at a time: the interval at which a hard stop (SetStopLevel) and the clock are looked at.
const cpuFuelChunk = 2048

// RequireCPU charges cpuAmount ticks to the context.  An instruction of the VM calls it, so it is made to cost a subtraction and a compare: the
// context is granted ticks in advance (grantFuel), up to the next point where it has to look at its limits, and RequireCPU only uses them up.
// Where the grant runs out (refuel) the ticks used are added to usedResources, the limits are checked and a new grant is made, so a limit
// stops the context at the same tick as if each one were checked, and what the context has used is exact when it is read (syncCPU).
func (m *runtimeContextManager) RequireCPU(cpuAmount uint64) {
	if m.trackCpu {
		m.fuel -= int64(cpuAmount) // amounts are sizes of things the VM works on, far below 2^62
		if m.fuel <= 0 {
			m.refuel()
		}
	}
}

// syncCPU adds the ticks used from the grant to usedResources.
func (m *runtimeContextManager) syncCPU() {
	m.usedResources.Cpu += uint64(m.granted - m.fuel)
	m.granted = m.fuel
}

// grantFuel grants the ticks to be used before the next time the limits are looked at: not more than what is left of the hard limit (so that the
// tick that reaches it is the one that finds out), of the time to the next look at the clock, and cpuFuelChunk.
func (m *runtimeContextManager) grantFuel() {
	if !m.trackCpu {
		m.fuel, m.granted = 0, 0
		return
	}
	grant := int64(cpuFuelChunk)
	used := m.usedResources.Cpu
	if limit := m.hardLimits.Cpu; limit > 0 && limit > used && int64(limit-used) < grant {
		grant = int64(limit - used)
	}
	if m.trackTime && m.nextCpuThreshold > used && int64(m.nextCpuThreshold-used) < grant {
		grant = int64(m.nextCpuThreshold - used)
	}
	if grant < 1 {
		grant = 1
	}
	m.fuel, m.granted = grant, grant
}

// refuel is where RequireCPU finds the grant used up: it does what a charge of each tick would have done at its end.
//
//go:noinline
func (m *runtimeContextManager) refuel() {
	if m.stopLevel&HardStop != 0 {
		m.KillContext()
	}
	cpuUsed := m.usedResources.Cpu + uint64(m.granted-m.fuel)
	m.usedResources.Cpu = cpuUsed
	m.fuel, m.granted = 0, 0
	if atLimit(cpuUsed, m.hardLimits.Cpu) {
		m.TerminateContext("CPU limit of %d exceeded", m.hardLimits.Cpu)
	}
	if m.trackTime && m.nextCpuThreshold <= cpuUsed {
		m.nextCpuThreshold = cpuUsed + cpuThresholdIncrement
		m.updateTimeUsed()
	}
	m.grantFuel()
}

func (m *runtimeContextManager) UnusedCPU() uint64 {
	m.syncCPU()
	return m.hardLimits.Cpu - m.usedResources.Cpu
}

func (m *runtimeContextManager) RequireMem(memAmount uint64) {
	if m.trackMem {
		// The path with limit is "outlined" so RequireMem can be inlined,
		// minimising the overhead when there is no limit.
		m.requireMem(memAmount)
	}
}

//go:noinline
func (m *runtimeContextManager) requireMem(memAmount uint64) {
	if m.stopLevel&HardStop != 0 {
		m.KillContext()
	}
	memUsed := m.usedResources.Memory + memAmount
	if atLimit(memUsed, m.hardLimits.Memory) {
		m.TerminateContext("memory limit of %d exceeded", m.hardLimits.Memory)
	}
	m.usedResources.Memory = memUsed
}

func (m *runtimeContextManager) RequireSize(sz uintptr) (mem uint64) {
	mem = uint64(sz)
	m.RequireMem(mem)
	return
}

func (m *runtimeContextManager) RequireArrSize(sz uintptr, n int) (mem uint64) {
	mem = uint64(sz) * uint64(n)
	m.RequireMem(mem)
	return
}

func (m *runtimeContextManager) RequireBytes(n int) (mem uint64) {
	mem = uint64(n)
	m.RequireMem(mem)
	return
}

func (m *runtimeContextManager) ReleaseMem(memAmount uint64) {
	// TODO: think about what to do when memory is released when unwinding from
	// a quota exceeded error
	if m.hardLimits.Memory > 0 {
		if memAmount <= m.usedResources.Memory {
			m.usedResources.Memory -= memAmount
		} else {
			panic("Too much mem released")
		}
	}
}

func (m *runtimeContextManager) ReleaseSize(sz uintptr) {
	m.ReleaseMem(uint64(sz))
}

func (m *runtimeContextManager) ReleaseArrSize(sz uintptr, n int) {
	m.ReleaseMem(uint64(sz) * uint64(n))
}

func (m *runtimeContextManager) ReleaseBytes(n int) {
	m.ReleaseMem(uint64(n))
}

func (m *runtimeContextManager) UnusedMem() uint64 {
	return m.hardLimits.Memory - m.usedResources.Memory
}

func (m *runtimeContextManager) updateTimeUsed() {
	m.usedResources.Millis = now() - m.startTime
	if atLimit(m.usedResources.Millis, m.hardLimits.Millis) {
		m.TerminateContext("time limit of %d exceeded", m.hardLimits.Millis)
	}
}

// LinearUnused returns an amount of resource combining memory and cpu.  It is
// useful when calling functions whose time complexity is a linear function of
// the size of their output.  As cpu ticks are "smaller" than memory ticks, the
// cpuFactor arguments allows specifying an increased "weight" for cpu ticks.
func (m *runtimeContextManager) LinearUnused(cpuFactor uint64) uint64 {
	mem := m.UnusedMem()
	cpu := m.UnusedCPU() * cpuFactor
	switch {
	case cpu == 0:
		return mem
	case mem == 0:
		return cpu
	case cpu > mem:
		return mem
	default:
		return cpu
	}
}

// LinearRequire can be used to actually consume (part of) the resource budget
// returned by LinearUnused (with the same cpuFactor).
func (m *runtimeContextManager) LinearRequire(cpuFactor uint64, amt uint64) {
	m.RequireMem(amt)
	m.RequireCPU(amt / cpuFactor)
}

// KillContext forcefully terminates the context with the message "force kill".
func (m *runtimeContextManager) KillContext() {
	m.TerminateContext("force kill")
}

// TerminateContext forcefully terminates the context with the given message.
func (m *runtimeContextManager) TerminateContext(format string, args ...interface{}) {
	if m.status != StatusLive {
		return
	}
	m.status = StatusKilled
	panic(ContextTerminationError{
		message: fmt.Sprintf(format, args...),
	})
}

// FSAccess returns the filesystem access allowed in the current context.
func (m *runtimeContextManager) FSAccess() *safeio.FSAccess {
	return m.fsAccess
}

// Current unix time in ms
func now() uint64 {
	return uint64(time.Now().UnixNano() / 1e6)
}
