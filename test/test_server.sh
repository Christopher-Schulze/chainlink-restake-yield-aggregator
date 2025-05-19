#!/bin/bash
# Simple test script for the Restake Yield External Adapter

# Set environment variables for testing
export PORT=8080
export TIMEOUT=10s
export AGGREGATION_MODE=weighted
export ENABLE_CIRCUIT_BREAKER=true
export ENABLE_VALIDATION=true
export ENABLE_METRICS=true
export ENABLE_ENTERPRISE_FEATURES=true
export RATE_LIMIT_RPS=50
export DATA_INTEGRITY_ENABLED=true
export METRICS_EXPORT_ENABLED=true
export MULTICHAIN_ENABLED=true
export POLYGON_ENABLED=true

# Build the application
echo "Building the application..."
go build -o ./bin/restake-yield-ea ./cmd/server

# Check build success
if [ $? -ne 0 ]; then
    echo "Build failed."
    exit 1
fi

# Run the server in background
echo "Starting server..."
./bin/restake-yield-ea &
SERVER_PID=$!

# Wait for server to start
sleep 2

# Check if server is running
if ! ps -p $SERVER_PID > /dev/null; then
    echo "Server failed to start."
    exit 1
fi

# Test basic health endpoint
echo "Testing health endpoint..."
HEALTH_RESPONSE=$(curl -s http://localhost:8080/health)
echo $HEALTH_RESPONSE

# Test Chainlink EA endpoint
echo "Testing Chainlink EA endpoint..."
RESPONSE=$(curl -s -X POST http://localhost:8080/ \
-H "Content-Type: application/json" \
-d '{
  "id": "test_request_1",
  "jobRunId": "1234567890",
  "data": {
    "test": true
  }
}')

echo $RESPONSE | jq .

# Check if enterprise features are present in the response
if [[ $RESPONSE == *"enterprise"* ]]; then
    echo "✅ Enterprise features detected in response"
else
    echo "❌ Enterprise features not detected"
fi

if [[ $RESPONSE == *"signed"* ]]; then
    echo "✅ Data integrity signing is working"
else
    echo "❌ Data integrity signing not working"
fi

if [[ $RESPONSE == *"multichain"* ]]; then
    echo "✅ Multi-chain support detected"
else
    echo "❌ Multi-chain support not detected"
fi

# Check metrics
echo "Testing metrics endpoint..."
METRICS_RESPONSE=$(curl -s http://localhost:8080/metrics)
echo "Metrics response length: $(echo $METRICS_RESPONSE | wc -c) bytes"

# Kill the server
echo "Stopping server..."
kill $SERVER_PID

echo "Test completed."
