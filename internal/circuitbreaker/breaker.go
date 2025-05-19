// Package circuitbreaker provides a defensive mechanism to protect against extreme market conditions 
// and erroneous data in the yield aggregation system.
package circuitbreaker

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/yourorg/restake-yield-ea/internal/model"
	"github.com/sirupsen/logrus"
)

// State represents the current state of the circuit breaker
type State int

// Circuit breaker states
const (
	StateClosed State = iota // Normal operation
	StateOpen              // Tripped, no new operations allowed
	StateHalfOpen          // Testing if system has recovered
)

// CircuitBreaker implements the circuit breaker pattern to prevent system overload
// and protect against abnormal market conditions or erroneous data.
type CircuitBreaker struct {
	// Configuration thresholds for triggering the circuit breaker
	thresholds Thresholds
	
	// Current state of the circuit breaker (Closed, Open, HalfOpen)
	state State
	
	// Timestamp of the last circuit trip
	lastTrip time.Time
	
	// Duration before auto-reset attempt
	resetDelay time.Duration
	
	// Mutex for thread safety
	mu sync.RWMutex
	
	// Historical metrics used for comparison and fallback
	metricsHistory []model.Metric
	
	// Count of consecutive successful operations in HalfOpen state
	successCount int
	
	// Number of successful operations required to close circuit
	successThreshold int
	
	// Event callback for monitoring/alerting
	onTripCallback func(reason string, metrics []model.Metric)
}

// Thresholds defines the limits that will trigger the circuit breaker
type Thresholds struct {
	// Maximum allowed APY value (e.g., 10.0 for 1000%)
	MaxAPY float64 `json:"max_apy"`
	
	// Maximum allowed change in TVL between consecutive checks (e.g., 0.5 for 50%)
	MaxTVLChange float64 `json:"max_tvl_change"`
	
	// Minimum number of providers required for valid aggregation
	MinProviders int `json:"min_providers"`
	
	// Maximum standard deviation for APY values as multiple of mean
	MaxStdDevMultiple float64 `json:"max_std_dev_multiple,omitempty"`
}

// New creates a new CircuitBreaker with the provided thresholds
func New(t Thresholds) *CircuitBreaker {
	return &CircuitBreaker{
		thresholds:       t,
		state:           StateClosed,
		resetDelay:      5 * time.Minute,
		successThreshold: 3,
	}
}

// WithResetDelay sets a custom reset delay and returns the circuit breaker
func (cb *CircuitBreaker) WithResetDelay(delay time.Duration) *CircuitBreaker {
	cb.resetDelay = delay
	return cb
}

// WithSuccessThreshold sets the number of successful operations needed to close the circuit
func (cb *CircuitBreaker) WithSuccessThreshold(threshold int) *CircuitBreaker {
	cb.successThreshold = threshold
	return cb
}

// WithTripCallback sets a callback function that is called when the circuit trips
func (cb *CircuitBreaker) WithTripCallback(callback func(reason string, metrics []model.Metric)) *CircuitBreaker {
	cb.onTripCallback = callback
	return cb
}

// Check evaluates the metrics against defined thresholds and determines if the operation should proceed.
// If the circuit is open, it blocks operations and returns an error.
// If the metrics violate thresholds, it trips the circuit and returns an error.
func (cb *CircuitBreaker) Check(metrics []model.Metric) error {
	// Acquire a read lock initially to check state
	cb.mu.RLock()
	state := cb.state
	lastTripTime := cb.lastTrip
	cb.mu.RUnlock()

	// If circuit is open, check if it's time for a reset attempt
	if state == StateOpen {
		if time.Since(lastTripTime) > cb.resetDelay {
			cb.transitionToHalfOpen()
		} else {
			return errors.New("circuit breaker open: system protection engaged")
		}
	}

	// Now get a write lock for the actual check and potential state modification
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Early exit for empty metrics
	if len(metrics) == 0 {
		return errors.New("no metrics provided to circuit breaker")
	}

	// Check if we have enough providers
	if len(metrics) < cb.thresholds.MinProviders {
		reason := fmt.Sprintf("insufficient provider count: got %d, need %d", 
			len(metrics), cb.thresholds.MinProviders)
		cb.trip(reason, metrics)
		return errors.New(reason)
	}

	// Check each metric for APY threshold violation
	for _, m := range metrics {
		if m.APY > cb.thresholds.MaxAPY {
			reason := fmt.Sprintf("APY exceeds maximum threshold: %f > %f", 
				m.APY, cb.thresholds.MaxAPY)
			cb.trip(reason, metrics)
			return errors.New(reason)
		}
	}

	// Check for drastic TVL changes if we have history
	if len(cb.metricsHistory) > 0 {
		lastMetric := cb.metricsHistory[len(cb.metricsHistory)-1]
		currentTVL := calculateAverageTVL(metrics)
		lastTVL := lastMetric.TVL
		
		// Only check if we have substantial TVL (avoid division by zero or small number issues)
		if lastTVL > 1.0 {
			changeRatio := math.Abs(currentTVL-lastTVL) / lastTVL
			if changeRatio > cb.thresholds.MaxTVLChange {
				reason := fmt.Sprintf("TVL change too drastic: %.2f%% (threshold: %.2f%%)", 
					changeRatio*100, cb.thresholds.MaxTVLChange*100)
				cb.trip(reason, metrics)
				return errors.New(reason)
			}
		}
	}
	
	// Check for excessive standard deviation in APY if threshold is set
	if cb.thresholds.MaxStdDevMultiple > 0 && len(metrics) > 1 {
		stdDev, mean := calculateStdDevAndMean(metrics)
		if mean > 0 && stdDev/mean > cb.thresholds.MaxStdDevMultiple {
			reason := fmt.Sprintf("APY standard deviation too high: %.2f x mean (threshold: %.2f)", 
				stdDev/mean, cb.thresholds.MaxStdDevMultiple)
			cb.trip(reason, metrics)
			return errors.New(reason)
		}
	}

	// All checks passed, record metrics and update state
	logrus.Debug("Circuit breaker checks passed")
	
	// Store these metrics for future comparison
	cb.addToHistory(metrics)
	
	// If we're in half-open state, increment success count and check if we can close
	if cb.state == StateHalfOpen {
		cb.successCount++
		if cb.successCount >= cb.successThreshold {
			cb.state = StateClosed
			cb.successCount = 0
			logrus.Info("Circuit breaker closed: system has recovered")
		}
	}
	
	return nil
}

// GetState returns the current state of the circuit breaker
func (cb *CircuitBreaker) GetState() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Reset forcibly resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = StateClosed
	cb.successCount = 0
	logrus.Info("Circuit breaker manually reset to closed state")
}

// LastGoodMetrics returns the most recent valid metrics stored in history
func (cb *CircuitBreaker) LastGoodMetrics() []model.Metric {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	
	if len(cb.metricsHistory) == 0 {
		return nil
	}
	
	// Return a copy of the last set of metrics to prevent external modification
	lastBatch := make([]model.Metric, len(cb.metricsHistory))
	copy(lastBatch, cb.metricsHistory)
	return lastBatch
}

// transitionToHalfOpen changes the circuit state to half-open for testing recovery
func (cb *CircuitBreaker) transitionToHalfOpen() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state == StateOpen {
		cb.state = StateHalfOpen
		cb.successCount = 0
		logrus.Info("Circuit breaker half-open: testing system recovery")
	}
}

// trip sets the circuit breaker to open state with the current time
func (cb *CircuitBreaker) trip(reason string, metrics []model.Metric) {
	cb.state = StateOpen
	cb.lastTrip = time.Now()
	logrus.Warnf("Circuit breaker tripped: %s", reason)
	
	// Call the callback if registered
	if cb.onTripCallback != nil {
		go cb.onTripCallback(reason, metrics)
	}
}

// addToHistory adds metrics to the history, maintaining a bounded size
func (cb *CircuitBreaker) addToHistory(metrics []model.Metric) {
	// Add a combined/average metric to history to keep it manageable
	avgMetric := model.Metric{
		Provider:    "aggregated",
		APY:         calculateAverageAPY(metrics),
		TVL:         calculateAverageTVL(metrics),
		CollectedAt: time.Now().Unix(),
	}
	
	cb.metricsHistory = append(cb.metricsHistory, avgMetric)
	
	// Keep history bounded to avoid memory growth
	const maxHistorySize = 100
	if len(cb.metricsHistory) > maxHistorySize {
		cb.metricsHistory = cb.metricsHistory[len(cb.metricsHistory)-maxHistorySize:]
	}
}

// calculateAverageAPY returns the weighted average APY from multiple metrics
func calculateAverageAPY(metrics []model.Metric) float64 {
	var totalAPY, totalTVL float64
	for _, m := range metrics {
		totalAPY += m.APY * m.TVL
		totalTVL += m.TVL
	}
	
	if totalTVL > 0 {
		return totalAPY / totalTVL
	}
	return 0
}

// calculateAverageTVL returns the total TVL from multiple metrics
func calculateAverageTVL(metrics []model.Metric) float64 {
	var totalTVL float64
	for _, m := range metrics {
		totalTVL += m.TVL
	}
	return totalTVL
}

// calculateStdDevAndMean computes the standard deviation and mean of APY values
func calculateStdDevAndMean(metrics []model.Metric) (float64, float64) {
	if len(metrics) <= 1 {
		return 0, 0
	}
	
	// Calculate mean
	var sum float64
	for _, m := range metrics {
		sum += m.APY
	}
	mean := sum / float64(len(metrics))
	
	// Calculate variance
	var variance float64
	for _, m := range metrics {
		diff := m.APY - mean
		variance += diff * diff
	}
	variance /= float64(len(metrics) - 1)
	
	// Return standard deviation and mean
	return math.Sqrt(variance), mean
}
