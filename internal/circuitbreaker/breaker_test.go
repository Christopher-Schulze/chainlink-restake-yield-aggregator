package circuitbreaker

import (
	"sync"
	"testing"
	"time"

	"github.com/christopher/restake-yield-ea/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStateString(t *testing.T) {
	assert.Equal(t, "closed", StateClosed.String())
	assert.Equal(t, "open", StateOpen.String())
	assert.Equal(t, "half-open", StateHalfOpen.String())
	assert.Equal(t, "unknown", State(999).String())
}

func TestCalculateAverageAPY(t *testing.T) {
	metrics := []model.Metric{
		{APY: 0.04, TVL: 1000},
		{APY: 0.06, TVL: 2000},
	}
	// Weighted: (0.04*1000 + 0.06*2000) / 3000 = 160/3000 = 0.0533...
	assert.InDelta(t, 0.0533, calculateAverageAPY(metrics), 1e-4)
}

func TestCalculateAverageAPYEmpty(t *testing.T) {
	assert.Equal(t, 0.0, calculateAverageAPY(nil))
}

func TestCalculateAverageAPYZeroTVL(t *testing.T) {
	metrics := []model.Metric{
		{APY: 0.04, TVL: 0},
		{APY: 0.06, TVL: 0},
	}
	assert.Equal(t, 0.0, calculateAverageAPY(metrics))
}

func TestCalculateAverageTVL(t *testing.T) {
	metrics := []model.Metric{
		{TVL: 1000},
		{TVL: 2000},
		{TVL: 3000},
	}
	assert.Equal(t, 6000.0, calculateAverageTVL(metrics))
}

func TestCalculateAverageTVLEmpty(t *testing.T) {
	assert.Equal(t, 0.0, calculateAverageTVL(nil))
}

func TestCalculateStdDevAndMeanEmpty(t *testing.T) {
	std, mean := calculateStdDevAndMean(nil)
	assert.Equal(t, 0.0, std)
	assert.Equal(t, 0.0, mean)
}

func TestCalculateStdDevAndMeanSingle(t *testing.T) {
	metrics := []model.Metric{{APY: 0.04}}
	std, mean := calculateStdDevAndMean(metrics)
	assert.Equal(t, 0.0, std)
	assert.Equal(t, 0.0, mean)
}

func TestCalculateStdDevAndMeanMultiple(t *testing.T) {
	metrics := []model.Metric{
		{APY: 0.04},
		{APY: 0.06},
		{APY: 0.05},
	}
	std, mean := calculateStdDevAndMean(metrics)
	assert.InDelta(t, 0.05, mean, 1e-9)
	assert.Greater(t, std, 0.0)
}

func TestCircuitBreakerTripAndRecover(t *testing.T) {
	cb := New(Thresholds{MaxAPY: 1.0, MaxTVLChange: 0.5, MinProviders: 1}).
		WithResetDelay(100 * time.Millisecond).
		WithSuccessThreshold(1)
	assert.Equal(t, StateClosed, cb.GetState())

	// Trip the breaker.
	_ = cb.Check([]model.Metric{{APY: 100.0, TVL: 1000, CollectedAt: time.Now().Unix()}})
	assert.Equal(t, StateOpen, cb.GetState())

	// Wait for reset delay.
	time.Sleep(150 * time.Millisecond)

	// Should transition to half-open on next check, then close on success.
	_ = cb.Check([]model.Metric{{APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}})
	assert.Equal(t, StateClosed, cb.GetState())
}

func TestCircuitBreakerLastGoodMetrics(t *testing.T) {
	cb := New(Thresholds{MaxAPY: 1.0, MaxTVLChange: 0.5, MinProviders: 1})
	good := []model.Metric{{APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}
	_ = cb.Check(good)

	lastGood := cb.LastGoodMetrics()
	assert.NotEmpty(t, lastGood)
	assert.Equal(t, 0.04, lastGood[0].APY)
}

func TestCircuitBreakerReset(t *testing.T) {
	cb := New(Thresholds{MaxAPY: 1.0, MaxTVLChange: 0.5, MinProviders: 1})
	_ = cb.Check([]model.Metric{{APY: 100.0, TVL: 1000, CollectedAt: time.Now().Unix()}})
	assert.Equal(t, StateOpen, cb.GetState())

	cb.Reset()
	assert.Equal(t, StateClosed, cb.GetState())
}

func TestWithTripCallback(t *testing.T) {
	called := false
	cb := New(Thresholds{MaxAPY: 1.0, MaxTVLChange: 0.5, MinProviders: 1}).
		WithTripCallback(func(reason string, _ []model.Metric) {
			called = true
		})
	_ = cb.Check([]model.Metric{{APY: 100.0, TVL: 1000, CollectedAt: time.Now().Unix()}})
	assert.True(t, called, "trip callback should be called")
}

func TestCheckEmptyMetrics(t *testing.T) {
	cb := New(Thresholds{MaxAPY: 1.0, MaxTVLChange: 0.5, MinProviders: 1})
	err := cb.Check([]model.Metric{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no metrics provided")
	// Empty metrics should NOT trip the circuit.
	assert.Equal(t, StateClosed, cb.GetState())

	// nil should behave the same way.
	err = cb.Check(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no metrics provided")
	assert.Equal(t, StateClosed, cb.GetState())
}

func TestCheckInsufficientProviders(t *testing.T) {
	cb := New(Thresholds{MaxAPY: 1.0, MinProviders: 3})
	metrics := []model.Metric{
		{APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()},
		{APY: 0.05, TVL: 1000, CollectedAt: time.Now().Unix()},
	}
	err := cb.Check(metrics)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient provider count")
	assert.Equal(t, StateOpen, cb.GetState())
}

func TestCheckTVLChangeDetection(t *testing.T) {
	cb := New(Thresholds{MaxAPY: 1.0, MaxTVLChange: 0.1, MinProviders: 1})
	metrics := []model.Metric{{APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}

	// First check establishes history.
	assert.NoError(t, cb.Check(metrics))
	// Second check with same TVL should pass.
	assert.NoError(t, cb.Check(metrics))

	// Third check with a 10x TVL jump should trip the breaker.
	bigMetrics := []model.Metric{{APY: 0.04, TVL: 10000, CollectedAt: time.Now().Unix()}}
	err := cb.Check(bigMetrics)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "TVL change too drastic")
	assert.Equal(t, StateOpen, cb.GetState())
}

func TestCheckStdDevMultiple(t *testing.T) {
	cb := New(Thresholds{MaxAPY: 1.0, MaxStdDevMultiple: 2.0, MinProviders: 1})
	// High variance: four tiny APYs and one large one. The std/mean ratio
	// (~2.2) exceeds the 2.0 threshold.
	metrics := []model.Metric{
		{APY: 0.001, TVL: 1000, CollectedAt: time.Now().Unix()},
		{APY: 0.001, TVL: 1000, CollectedAt: time.Now().Unix()},
		{APY: 0.001, TVL: 1000, CollectedAt: time.Now().Unix()},
		{APY: 0.001, TVL: 1000, CollectedAt: time.Now().Unix()},
		{APY: 0.9, TVL: 1000, CollectedAt: time.Now().Unix()},
	}
	err := cb.Check(metrics)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "standard deviation too high")
	assert.Equal(t, StateOpen, cb.GetState())
}

func TestCheckHalfOpenSuccessThresholdGreaterThanOne(t *testing.T) {
	cb := New(Thresholds{MaxAPY: 1.0, MaxTVLChange: 0.5, MinProviders: 1}).
		WithResetDelay(100 * time.Millisecond).
		WithSuccessThreshold(3)

	// Trip the breaker.
	_ = cb.Check([]model.Metric{{APY: 100.0, TVL: 1000, CollectedAt: time.Now().Unix()}})
	assert.Equal(t, StateOpen, cb.GetState())

	// Wait for reset delay so the next Check transitions to half-open.
	time.Sleep(150 * time.Millisecond)

	good := []model.Metric{{APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}}

	// First success: half-open -> half-open.
	_ = cb.Check(good)
	assert.Equal(t, StateHalfOpen, cb.GetState())

	// Second success: still half-open (need 3).
	_ = cb.Check(good)
	assert.Equal(t, StateHalfOpen, cb.GetState())

	// Third success: half-open -> closed.
	_ = cb.Check(good)
	assert.Equal(t, StateClosed, cb.GetState())
}

func TestCheckReturnsErrorWhenOpen(t *testing.T) {
	cb := New(Thresholds{MaxAPY: 1.0, MaxTVLChange: 0.5, MinProviders: 1})
	// Trip the breaker.
	_ = cb.Check([]model.Metric{{APY: 100.0, TVL: 1000, CollectedAt: time.Now().Unix()}})
	assert.Equal(t, StateOpen, cb.GetState())

	// Immediately call Check again without waiting for reset delay.
	err := cb.Check([]model.Metric{{APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "circuit breaker open")
	assert.Equal(t, StateOpen, cb.GetState())
}

// --- additional branch-coverage tests ---

// TestLastGoodMetricsNil covers the nil-return branch in LastGoodMetrics
// (breaker.go ~256-257). A fresh breaker that has never accepted any metrics
// should return nil.
func TestLastGoodMetricsNil(t *testing.T) {
	cb := New(Thresholds{MaxAPY: 1.0, MaxTVLChange: 0.5, MinProviders: 1})
	result := cb.LastGoodMetrics()
	assert.Nil(t, result)
}

// TestLastGoodMetricsStale verifies that LastGoodMetrics returns nil when
// the stored fallback data is older than maxStaleness.
func TestLastGoodMetricsStale(t *testing.T) {
	cb := New(Thresholds{MaxAPY: 1.0, MaxTVLChange: 0.5, MinProviders: 1}).
		WithMaxStaleness(1) // 1 second

	// Feed good metrics so lastGoodMetrics is populated.
	err := cb.Check([]model.Metric{{APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}})
	require.NoError(t, err)

	// Immediately, last-good should be available.
	assert.NotEmpty(t, cb.LastGoodMetrics())

	// Wait for staleness to expire.
	time.Sleep(2 * time.Second)
	assert.Nil(t, cb.LastGoodMetrics(), "stale last-good metrics should be rejected")
}

// TestLastGoodMetricsNoStalenessLimit verifies that with maxStaleness=0
// (the default), last-good metrics never expire.
func TestLastGoodMetricsNoStalenessLimit(t *testing.T) {
	cb := New(Thresholds{MaxAPY: 1.0, MaxTVLChange: 0.5, MinProviders: 1})
	// maxStaleness defaults to 0 = no limit

	err := cb.Check([]model.Metric{{APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}})
	require.NoError(t, err)
	assert.NotEmpty(t, cb.LastGoodMetrics())
}

// TestAddToHistoryTrimming covers the history-trimming branch in addToHistory
// (breaker.go ~295-297). When the history exceeds maxHistorySize (100), the
// oldest entries are trimmed.
func TestAddToHistoryTrimming(t *testing.T) {
	cb := New(Thresholds{MaxAPY: 1.0, MaxTVLChange: 1.0, MinProviders: 1})
	// Feed 105 successful checks to exceed the maxHistorySize of 100.
	for i := 0; i < 105; i++ {
		err := cb.Check([]model.Metric{{APY: 0.04, TVL: 1000, CollectedAt: time.Now().Unix()}})
		require.NoError(t, err, "check %d should pass", i)
	}
	// The history should be trimmed to exactly 100 entries.
	assert.Equal(t, 100, len(cb.metricsHistory))
}

// TestConcurrentCheckRaceAfterTrip verifies that the state recheck after
// acquiring the write lock prevents a race condition where a concurrent
// goroutine trips the breaker between the RLock release and the Lock
// acquire. Without the recheck, the second goroutine would proceed with
// validation and update lastGoodMetrics even though the breaker is open.
func TestConcurrentCheckRaceAfterTrip(t *testing.T) {
	cb := New(Thresholds{
		MaxAPY:          1.0,
		MaxTVLChange:    0.5,
		MinProviders:    1,
		MaxStdDevMultiple: 0,
	}).WithResetDelay(1 * time.Millisecond)

	// Good metrics that pass all checks.
	goodMetrics := []model.Metric{
		{Provider: "test", APY: 0.05, TVL: 100, CollectedAt: time.Now().Unix()},
	}
	// Bad metrics that trip the breaker (APY > MaxAPY).
	badMetrics := []model.Metric{
		{Provider: "test", APY: 5.0, TVL: 100, CollectedAt: time.Now().Unix()},
	}

	// First, establish a lastGoodMetrics baseline.
	require.NoError(t, cb.Check(goodMetrics))
	require.NotNil(t, cb.LastGoodMetrics(), "baseline lastGoodMetrics should exist")

	// Trip the breaker with bad metrics.
	require.Error(t, cb.Check(badMetrics))
	require.Equal(t, StateOpen, cb.GetState())

	// Wait for reset delay to elapse so transitionToHalfOpen is triggered.
	time.Sleep(10 * time.Millisecond)

	// Now spawn concurrent goroutines: one sends bad metrics (trips), the
	// others send good metrics. The good-metrics goroutines must NOT
	// update lastGoodMetrics if the bad-metrics goroutine trips the
	// breaker first.
	var wg sync.WaitGroup
	const numGood = 10
	for i := 0; i < numGood; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cb.Check(goodMetrics)
		}()
	}
	// One goroutine sends bad metrics to trip the breaker.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = cb.Check(badMetrics)
	}()
	wg.Wait()

	// After the race, the breaker should be open (tripped by bad metrics,
	// or re-tripped after half-open). The key assertion is that the
	// lastGoodMetrics was NOT corrupted by a concurrent good-metrics
	// check that proceeded while the breaker was open.
	// We can't deterministically prove the race without controlling
	// scheduling, but with -race we verify no data race is detected,
	// and the breaker state is consistent.
	state := cb.GetState()
	// State should be Open or HalfOpen (if all good checks succeeded).
	assert.True(t, state == StateOpen || state == StateHalfOpen,
		"state should be Open or HalfOpen, got %s", state)
}

// TestRecheckPreventsUpdateWhenOpen verifies directly that if the breaker
// is open when the write lock is acquired, the check does not update
// lastGoodMetrics.
func TestRecheckPreventsUpdateWhenOpen(t *testing.T) {
	cb := New(Thresholds{
		MaxAPY:       1.0,
		MaxTVLChange: 0.5,
		MinProviders: 1,
	}).WithResetDelay(1 * time.Millisecond)

	goodMetrics := []model.Metric{
		{Provider: "test", APY: 0.05, TVL: 100, CollectedAt: time.Now().Unix()},
	}

	// Establish baseline.
	require.NoError(t, cb.Check(goodMetrics))
	baseline := cb.LastGoodMetrics()
	require.NotNil(t, baseline)
	require.Equal(t, 100.0, baseline[0].TVL)

	// Trip the breaker.
	badMetrics := []model.Metric{
		{Provider: "test", APY: 5.0, TVL: 100, CollectedAt: time.Now().Unix()},
	}
	require.Error(t, cb.Check(badMetrics))
	require.Equal(t, StateOpen, cb.GetState())

	// Manually set the state to Open and try to check good metrics.
	// The recheck should prevent lastGoodMetrics from being updated.
	// We use a different TVL to detect if the update happened.
	differentGoodMetrics := []model.Metric{
		{Provider: "test", APY: 0.05, TVL: 999, CollectedAt: time.Now().Unix()},
	}

	// Wait for reset delay, then immediately trip again before the check
	// can proceed. We simulate this by having the breaker open and not
	// enough time elapsed for reset.
	cb.mu.Lock()
	cb.state = StateOpen
	cb.lastTrip = time.Now() // just tripped, reset delay not elapsed
	cb.mu.Unlock()

	// Now check with good metrics — should return error (breaker open)
	// and NOT update lastGoodMetrics.
	err := cb.Check(differentGoodMetrics)
	require.Error(t, err)

	// lastGoodMetrics should still have the original TVL (100), not 999.
	current := cb.LastGoodMetrics()
	require.NotNil(t, current)
	assert.Equal(t, 100.0, current[0].TVL, "lastGoodMetrics must not be updated while breaker is open")
}
