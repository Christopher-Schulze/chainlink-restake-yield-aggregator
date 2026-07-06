package envx

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestString(t *testing.T) {
	t.Setenv("ENVX_TEST_STR", "hello")
	assert.Equal(t, "hello", String("ENVX_TEST_STR", "default"))
	assert.Equal(t, "default", String("ENVX_TEST_UNSET", "default"))
}

func TestInt(t *testing.T) {
	t.Setenv("ENVX_TEST_INT", "42")
	assert.Equal(t, 42, Int("ENVX_TEST_INT", 0))
	assert.Equal(t, 0, Int("ENVX_TEST_UNSET", 0))
	t.Setenv("ENVX_TEST_INT_BAD", "not-a-number")
	assert.Equal(t, 99, Int("ENVX_TEST_INT_BAD", 99))
}

func TestInt64(t *testing.T) {
	t.Setenv("ENVX_TEST_INT64", "9999999999")
	assert.Equal(t, int64(9999999999), Int64("ENVX_TEST_INT64", 0))
	assert.Equal(t, int64(0), Int64("ENVX_TEST_UNSET", 0))
}

func TestFloat64(t *testing.T) {
	t.Setenv("ENVX_TEST_FLOAT", "3.14")
	assert.Equal(t, 3.14, Float64("ENVX_TEST_FLOAT", 0))
	assert.Equal(t, 0.0, Float64("ENVX_TEST_UNSET", 0))
	t.Setenv("ENVX_TEST_FLOAT_BAD", "not-a-float")
	assert.Equal(t, 1.5, Float64("ENVX_TEST_FLOAT_BAD", 1.5))
}

func TestDuration(t *testing.T) {
	t.Setenv("ENVX_TEST_DUR", "30s")
	assert.Equal(t, 30*time.Second, Duration("ENVX_TEST_DUR", 0))
	assert.Equal(t, time.Duration(0), Duration("ENVX_TEST_UNSET", 0))
	t.Setenv("ENVX_TEST_DUR_BAD", "not-a-duration")
	assert.Equal(t, 5*time.Second, Duration("ENVX_TEST_DUR_BAD", 5*time.Second))
}

func TestBool(t *testing.T) {
	t.Setenv("ENVX_TEST_BOOL", "true")
	assert.Equal(t, true, Bool("ENVX_TEST_BOOL", false))
	assert.Equal(t, false, Bool("ENVX_TEST_UNSET", false))
	t.Setenv("ENVX_TEST_BOOL_BAD", "not-a-bool")
	assert.Equal(t, true, Bool("ENVX_TEST_BOOL_BAD", true))
}
