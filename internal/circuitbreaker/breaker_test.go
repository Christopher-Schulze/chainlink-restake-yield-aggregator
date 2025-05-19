package circuitbreaker

import (
	"testing"
	"time"

	"github.com/yourorg/restake-yield-ea/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCircuitBreaker_BasicFunctionality(t *testing.T) {
	thresholds := Thresholds{
		MaxAPY:          5.0,  // 500% max APY
		MaxTVLChange:    0.3,  // 30% max TVL change
		MinProviders:    2,    // Min 2 providers
		MaxStdDevMultiple: 3.0, // Standard deviation shouldn't exceed 3x mean
	}
	
	cb := New(thresholds).WithResetDelay(50 * time.Millisecond)
	assert.Equal(t, StateClosed, cb.GetState(), "Circuit breaker should start closed")
	
	// Valid metrics should pass
	validMetrics := []model.Metric{
		{Provider: "provider1", APY: 3.0, TVL: 1000, CollectedAt: time.Now().Unix()},
		{Provider: "provider2", APY: 4.0, TVL: 2000, CollectedAt: time.Now().Unix()},
	}
	
	err := cb.Check(validMetrics)
	assert.NoError(t, err, "Valid metrics should pass checks")
	assert.Equal(t, StateClosed, cb.GetState(), "Circuit should remain closed for valid metrics")
}

func TestCircuitBreaker_APYThreshold(t *testing.T) {
	thresholds := Thresholds{
		MaxAPY:       5.0,
		MaxTVLChange: 0.3,
		MinProviders: 2,
	}
	
	cb := New(thresholds)
	
	// Metrics with excessive APY should trip the circuit
	invalidMetrics := []model.Metric{
		{Provider: "provider1", APY: 3.0, TVL: 1000, CollectedAt: time.Now().Unix()},
		{Provider: "provider2", APY: 6.0, TVL: 2000, CollectedAt: time.Now().Unix()}, // Exceeds MaxAPY
	}
	
	err := cb.Check(invalidMetrics)
	assert.Error(t, err, "Excessive APY should trip the circuit")
	assert.Equal(t, StateOpen, cb.GetState(), "Circuit should be open after trip")
	assert.Contains(t, err.Error(), "APY exceeds maximum threshold", "Error should mention APY threshold")
}

func TestCircuitBreaker_TVLChange(t *testing.T) {
	thresholds := Thresholds{
		MaxAPY:       5.0,
		MaxTVLChange: 0.3,
		MinProviders: 2,
	}
	
	cb := New(thresholds)
	
	// First set of metrics to establish baseline
	baselineMetrics := []model.Metric{
		{Provider: "provider1", APY: 3.0, TVL: 1000, CollectedAt: time.Now().Unix()},
		{Provider: "provider2", APY: 4.0, TVL: 2000, CollectedAt: time.Now().Unix()},
	}
	
	err := cb.Check(baselineMetrics)
	require.NoError(t, err, "Baseline metrics should pass")
	
	// Second set with drastic TVL change
	changedMetrics := []model.Metric{
		{Provider: "provider1", APY: 3.0, TVL: 400, CollectedAt: time.Now().Unix()}, // 60% drop
		{Provider: "provider2", APY: 4.0, TVL: 800, CollectedAt: time.Now().Unix()}, // 60% drop
	}
	
	err = cb.Check(changedMetrics)
	assert.Error(t, err, "Drastic TVL change should trip the circuit")
	assert.Contains(t, err.Error(), "TVL change too drastic", "Error should mention TVL change")
}

func TestCircuitBreaker_InsufficientProviders(t *testing.T) {
	thresholds := Thresholds{
		MaxAPY:       5.0,
		MaxTVLChange: 0.3,
		MinProviders: 2,
	}
	
	cb := New(thresholds)
	
	// Only one provider (should be minimum 2)
	insufficientMetrics := []model.Metric{
		{Provider: "provider1", APY: 3.0, TVL: 1000, CollectedAt: time.Now().Unix()},
	}
	
	err := cb.Check(insufficientMetrics)
	assert.Error(t, err, "Insufficient provider count should trip the circuit")
	assert.Contains(t, err.Error(), "insufficient provider count", "Error should mention provider count")
}

func TestCircuitBreaker_Recovery(t *testing.T) {
	thresholds := Thresholds{
		MaxAPY:       5.0,
		MaxTVLChange: 0.3,
		MinProviders: 2,
	}
	
	cb := New(thresholds).
		WithResetDelay(50 * time.Millisecond).
		WithSuccessThreshold(1)
	
	// Trip the circuit
	invalidMetrics := []model.Metric{
		{Provider: "provider1", APY: 6.0, TVL: 1000, CollectedAt: time.Now().Unix()}, // Exceeds MaxAPY
		{Provider: "provider2", APY: 4.0, TVL: 2000, CollectedAt: time.Now().Unix()},
	}
	
	err := cb.Check(invalidMetrics)
	require.Error(t, err, "Should trip circuit with invalid metrics")
	assert.Equal(t, StateOpen, cb.GetState(), "Circuit should be open after trip")
	
	// Wait for reset delay
	time.Sleep(60 * time.Millisecond)
	
	// Valid metrics after reset delay should transition to half-open
	validMetrics := []model.Metric{
		{Provider: "provider1", APY: 3.0, TVL: 1000, CollectedAt: time.Now().Unix()},
		{Provider: "provider2", APY: 4.0, TVL: 2000, CollectedAt: time.Now().Unix()},
	}
	
	err = cb.Check(validMetrics)
	assert.NoError(t, err, "Valid metrics should pass in half-open state")
	assert.Equal(t, StateClosed, cb.GetState(), "Circuit should close after successful check in half-open state")
}

func TestCircuitBreaker_LastGoodMetrics(t *testing.T) {
	thresholds := Thresholds{
		MaxAPY:       5.0,
		MaxTVLChange: 0.3,
		MinProviders: 2,
	}
	
	cb := New(thresholds)
	
	// No metrics yet
	assert.Nil(t, cb.LastGoodMetrics(), "LastGoodMetrics should return nil if no metrics exist")
	
	// Add valid metrics
	validMetrics := []model.Metric{
		{Provider: "provider1", APY: 3.0, TVL: 1000, CollectedAt: time.Now().Unix()},
		{Provider: "provider2", APY: 4.0, TVL: 2000, CollectedAt: time.Now().Unix()},
	}
	
	err := cb.Check(validMetrics)
	require.NoError(t, err, "Valid metrics should pass")
	
	lastGood := cb.LastGoodMetrics()
	require.NotNil(t, lastGood, "LastGoodMetrics should return metrics after successful check")
	assert.NotEmpty(t, lastGood, "LastGoodMetrics should not be empty")
}

func TestCircuitBreaker_CallbackExecution(t *testing.T) {
	thresholds := Thresholds{
		MaxAPY:       5.0,
		MaxTVLChange: 0.3,
		MinProviders: 2,
	}
	
	callbackExecuted := false
	callbackReason := ""
	
	cb := New(thresholds).WithTripCallback(func(reason string, metrics []model.Metric) {
		callbackExecuted = true
		callbackReason = reason
	})
	
	// Trip the circuit
	invalidMetrics := []model.Metric{
		{Provider: "provider1", APY: 6.0, TVL: 1000, CollectedAt: time.Now().Unix()}, // Exceeds MaxAPY
		{Provider: "provider2", APY: 4.0, TVL: 2000, CollectedAt: time.Now().Unix()},
	}
	
	err := cb.Check(invalidMetrics)
	require.Error(t, err, "Should trip circuit with invalid metrics")
	
	// The callback executes in a goroutine, so give it a moment
	time.Sleep(5 * time.Millisecond)
	
	assert.True(t, callbackExecuted, "Callback should be executed when circuit trips")
	assert.Contains(t, callbackReason, "APY exceeds maximum threshold", "Callback reason should explain the trip")
}

func TestCircuitBreaker_ManualReset(t *testing.T) {
	thresholds := Thresholds{
		MaxAPY:       5.0,
		MaxTVLChange: 0.3,
		MinProviders: 2,
	}
	
	cb := New(thresholds)
	
	// Trip the circuit
	invalidMetrics := []model.Metric{
		{Provider: "provider1", APY: 6.0, TVL: 1000, CollectedAt: time.Now().Unix()}, // Exceeds MaxAPY
		{Provider: "provider2", APY: 4.0, TVL: 2000, CollectedAt: time.Now().Unix()},
	}
	
	err := cb.Check(invalidMetrics)
	require.Error(t, err, "Should trip circuit with invalid metrics")
	assert.Equal(t, StateOpen, cb.GetState(), "Circuit should be open after trip")
	
	// Manually reset
	cb.Reset()
	assert.Equal(t, StateClosed, cb.GetState(), "Circuit should be closed after manual reset")
	
	// Should accept valid metrics now
	validMetrics := []model.Metric{
		{Provider: "provider1", APY: 3.0, TVL: 1000, CollectedAt: time.Now().Unix()},
		{Provider: "provider2", APY: 4.0, TVL: 2000, CollectedAt: time.Now().Unix()},
	}
	
	err = cb.Check(validMetrics)
	assert.NoError(t, err, "Valid metrics should pass after manual reset")
}

func TestCircuitBreaker_StdDevCheck(t *testing.T) {
	thresholds := Thresholds{
		MaxAPY:            5.0,
		MaxTVLChange:      0.3,
		MinProviders:      2,
		MaxStdDevMultiple: 0.5, // Standard deviation shouldn't exceed 0.5x mean
	}
	
	cb := New(thresholds)
	
	// Consistent metrics should pass
	consistentMetrics := []model.Metric{
		{Provider: "provider1", APY: 3.0, TVL: 1000, CollectedAt: time.Now().Unix()},
		{Provider: "provider2", APY: 3.2, TVL: 2000, CollectedAt: time.Now().Unix()},
		{Provider: "provider3", APY: 2.8, TVL: 1500, CollectedAt: time.Now().Unix()},
	}
	
	err := cb.Check(consistentMetrics)
	assert.NoError(t, err, "Consistent metrics should pass std dev check")
	
	// Highly divergent metrics should trip the circuit
	divergentMetrics := []model.Metric{
		{Provider: "provider1", APY: 1.0, TVL: 1000, CollectedAt: time.Now().Unix()},
		{Provider: "provider2", APY: 5.0, TVL: 2000, CollectedAt: time.Now().Unix()}, // Big outlier
		{Provider: "provider3", APY: 1.2, TVL: 1500, CollectedAt: time.Now().Unix()},
	}
	
	cb.Reset() // Reset from previous tests
	err = cb.Check(divergentMetrics)
	assert.Error(t, err, "Divergent metrics should trip the circuit")
	assert.Contains(t, err.Error(), "APY standard deviation too high", "Error should mention standard deviation")
}

func TestCircuitBreaker_EmptyMetrics(t *testing.T) {
	thresholds := Thresholds{
		MaxAPY:       5.0,
		MaxTVLChange: 0.3,
		MinProviders: 2,
	}
	
	cb := New(thresholds)
	
	// Empty metrics should error
	err := cb.Check([]model.Metric{})
	assert.Error(t, err, "Empty metrics should cause error")
	assert.Contains(t, err.Error(), "no metrics provided", "Error should mention lack of metrics")
}
