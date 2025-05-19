package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/yourorg/restake-yield-ea/internal/security"
	"github.com/yourorg/restake-yield-ea/internal/types"
)

// EnterpriseConfig extends the base configuration with enterprise-grade features
type EnterpriseConfig struct {
	// Base configuration
	BaseConfig Config `json:"base"`
	
	// Multi-chain support
	ChainConfigs map[string]ChainConfig `json:"chains"`
	
	// Enterprise metrics export
	MetricsExport ExporterConfig `json:"metrics_export"`
	
	// Data integrity and cryptographic verification
	DataIntegrity VerificationConfig `json:"data_integrity"`
	
	// Advanced rate limiting and quotas
	RateLimiting RateLimitConfig `json:"rate_limiting"`
	
	// Chainlink OCR support
	OCR OCRConfig `json:"ocr"`
}

// ChainConfig is an alias for types.ChainConfig with an additional Providers field
type ChainConfig struct {
	types.ChainConfig
	Providers []string `json:"providers"`
}

// ExporterConfig defines settings for enterprise metrics export
type ExporterConfig struct {
	Enabled         bool     `json:"enabled"`
	BatchSize       int      `json:"batch_size"`
	ExportInterval  string   `json:"export_interval"`
	DashboardURL    string   `json:"dashboard_url"`
	
	// AWS settings
	AWSEnabled      bool     `json:"aws_enabled"`
	AWSRegion       string   `json:"aws_region"`
	AWSAccessKey    string   `json:"aws_access_key,omitempty"`
	AWSSecretKey    string   `json:"aws_secret_key,omitempty"`
	CloudwatchGroup string   `json:"cloudwatch_group"`
	S3Bucket        string   `json:"s3_bucket"`
	S3KeyPrefix     string   `json:"s3_key_prefix"`
	
	// Webhook settings
	WebhookEnabled  bool     `json:"webhook_enabled"`
	WebhookURL      string   `json:"webhook_url"`
	WebhookAPIKey   string   `json:"webhook_api_key,omitempty"`
	WebhookFormat   string   `json:"webhook_format"`
	
	// Kafka settings
	KafkaEnabled    bool     `json:"kafka_enabled"`
	KafkaBrokers    []string `json:"kafka_brokers"`
	KafkaTopic      string   `json:"kafka_topic"`
	KafkaUsername   string   `json:"kafka_username,omitempty"`
	KafkaPassword   string   `json:"kafka_password,omitempty"`
}

// VerificationConfig defines settings for data integrity and verification
type VerificationConfig struct {
	SignatureEnabled     bool   `json:"signature_enabled"`
	VerificationRequired bool   `json:"verification_required"`
	SignatureValidity    string `json:"signature_validity"`
	StrictMode           bool   `json:"strict_mode"`
	BlockchainVerification bool  `json:"blockchain_verification"`
	VerificationContract string `json:"verification_contract,omitempty"`
}

// RateLimitConfig defines settings for rate limiting and quotas
type RateLimitConfig struct {
	Enabled         bool   `json:"enabled"`
	RequestsPerMin  int    `json:"requests_per_min"`
	BurstSize       int    `json:"burst_size"`
	QuotaPerDay     int    `json:"quota_per_day"`
	APIKeyRequired  bool   `json:"api_key_required"`
	APIKeysFilePath string `json:"api_keys_file_path,omitempty"`
}

// OCRConfig defines settings for Chainlink Off-Chain Reporting
type OCRConfig struct {
	Enabled               bool   `json:"enabled"`
	ContractAddress       string `json:"contract_address,omitempty"`
	TransmitterAddress    string `json:"transmitter_address,omitempty"`
	KeyBundleID           string `json:"key_bundle_id,omitempty"`
	MonitoringEndpoint    string `json:"monitoring_endpoint,omitempty"`
	ObservationTimeout    string `json:"observation_timeout"`
	BlockchainTimeout     string `json:"blockchain_timeout"`
	ContractTransmitCount uint64 `json:"contract_transmit_count"`
	ObservationGracePeriod string `json:"observation_grace_period"`
}

// LoadEnterpriseConfig loads the enterprise configuration from JSON file
func LoadEnterpriseConfig(configPath string) (*EnterpriseConfig, error) {
	// Default configuration
	config := DefaultEnterpriseConfig()
	
	// If no path is specified, use environment variables
	if configPath == "" {
		return loadFromEnv(config)
	}
	
	// Load from file
	fileData, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	
	if err := json.Unmarshal(fileData, config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}
	
	// Apply any environment variable overrides
	config = applyEnvOverrides(config)
	
	logrus.Infof("Loaded enterprise configuration from %s", configPath)
	return config, nil
}

// DefaultEnterpriseConfig returns a default enterprise configuration
func DefaultEnterpriseConfig() *EnterpriseConfig {
	return &EnterpriseConfig{
		BaseConfig: DefaultConfig(),
		ChainConfigs: map[string]ChainConfig{
			"ethereum": {
				Enabled:     true,
				RPCEndpoint: "https://mainnet.infura.io/v3/YOUR_INFURA_KEY",
				APIEndpoint: "https://api.eigenlayer.xyz",
				Weight:      1.0,
				Providers:   []string{"eigenlayer", "stakewise"},
			},
		},
		MetricsExport: ExporterConfig{
			Enabled:        false,
			BatchSize:      100,
			ExportInterval: "1m",
		},
		DataIntegrity: VerificationConfig{
			SignatureEnabled:     true,
			VerificationRequired: true,
			SignatureValidity:    "24h",
			StrictMode:           false,
		},
		RateLimiting: RateLimitConfig{
			Enabled:        true,
			RequestsPerMin: 60,
			BurstSize:      10,
			QuotaPerDay:    10000,
			APIKeyRequired: false,
		},
		OCR: OCRConfig{
			Enabled:            false,
			ObservationTimeout: "10s",
			BlockchainTimeout:  "20s",
			ObservationGracePeriod: "2s",
		},
	}
}

// loadFromEnv loads configuration from environment variables
func loadFromEnv(config *EnterpriseConfig) (*EnterpriseConfig, error) {
	// Load base config from environment
	baseConfig, err := LoadConfigFromEnv()
	if err != nil {
		return nil, err
	}
	config.BaseConfig = *baseConfig
	
	// Load chain configurations
	chains := os.Getenv("SUPPORTED_CHAINS")
	if chains != "" {
		chainNames := strings.Split(chains, ",")
		for _, chain := range chainNames {
			chain = strings.TrimSpace(chain)
			envPrefix := "CHAIN_" + strings.ToUpper(chain) + "_"
			
			config.ChainConfigs[chain] = ChainConfig{
				Enabled:       getEnvBool(envPrefix+"ENABLED", true),
				RPCEndpoint:   os.Getenv(envPrefix+"RPC_ENDPOINT"),
				APIEndpoint:   os.Getenv(envPrefix+"API_ENDPOINT"),
				APIKey:        os.Getenv(envPrefix+"API_KEY"),
				Weight:        getEnvFloat(envPrefix+"WEIGHT", 1.0),
				GasMultiplier: getEnvFloat(envPrefix+"GAS_MULTIPLE", 1.0),
				Providers:     strings.Split(os.Getenv(envPrefix+"PROVIDERS"), ","),
			}
		}
	}
	
	// Load metrics export config
	config.MetricsExport.Enabled = getEnvBool("METRICS_EXPORT_ENABLED", false)
	config.MetricsExport.BatchSize = getEnvInt("METRICS_EXPORT_BATCH_SIZE", 100)
	config.MetricsExport.ExportInterval = os.Getenv("METRICS_EXPORT_INTERVAL")
	
	// AWS settings
	config.MetricsExport.AWSEnabled = getEnvBool("AWS_ENABLED", false)
	config.MetricsExport.AWSRegion = os.Getenv("AWS_REGION")
	config.MetricsExport.AWSAccessKey = os.Getenv("AWS_ACCESS_KEY")
	config.MetricsExport.AWSSecretKey = os.Getenv("AWS_SECRET_KEY")
	config.MetricsExport.S3Bucket = os.Getenv("S3_BUCKET")
	
	// Webhook settings
	config.MetricsExport.WebhookEnabled = getEnvBool("WEBHOOK_ENABLED", false)
	config.MetricsExport.WebhookURL = os.Getenv("WEBHOOK_URL")
	config.MetricsExport.WebhookAPIKey = os.Getenv("WEBHOOK_API_KEY")
	
	// Kafka settings
	config.MetricsExport.KafkaEnabled = getEnvBool("KAFKA_ENABLED", false)
	kafkaBrokers := os.Getenv("KAFKA_BROKERS")
	if kafkaBrokers != "" {
		config.MetricsExport.KafkaBrokers = strings.Split(kafkaBrokers, ",")
	}
	config.MetricsExport.KafkaTopic = os.Getenv("KAFKA_TOPIC")
	
	// Data integrity settings
	config.DataIntegrity.SignatureEnabled = getEnvBool("SIGNATURE_ENABLED", true)
	config.DataIntegrity.VerificationRequired = getEnvBool("VERIFICATION_REQUIRED", true)
	config.DataIntegrity.SignatureValidity = os.Getenv("SIGNATURE_VALIDITY")
	config.DataIntegrity.StrictMode = getEnvBool("STRICT_MODE", false)
	
	// Rate limiting settings
	config.RateLimiting.Enabled = getEnvBool("RATE_LIMIT_ENABLED", true)
	config.RateLimiting.RequestsPerMin = getEnvInt("REQUESTS_PER_MIN", 60)
	config.RateLimiting.APIKeyRequired = getEnvBool("API_KEY_REQUIRED", false)
	
	// OCR settings
	config.OCR.Enabled = getEnvBool("OCR_ENABLED", false)
	config.OCR.ContractAddress = os.Getenv("OCR_CONTRACT_ADDRESS")
	config.OCR.TransmitterAddress = os.Getenv("OCR_TRANSMITTER_ADDRESS")
	
	return config, nil
}

// applyEnvOverrides applies environment variable overrides to the loaded configuration
func applyEnvOverrides(config *EnterpriseConfig) *EnterpriseConfig {
	// Override base config
	if port := os.Getenv("PORT"); port != "" {
		config.BaseConfig.Port = port
	}
	
	if timeout := os.Getenv("TIMEOUT"); timeout != "" {
		config.BaseConfig.TimeoutStr = timeout
	}
	
	if logLevel := os.Getenv("LOG_LEVEL"); logLevel != "" {
		config.BaseConfig.LogLevel = logLevel
	}
	
	// Override any sensitive information
	for chainName, chainConfig := range config.ChainConfigs {
		envPrefix := "CHAIN_" + strings.ToUpper(chainName) + "_"
		
		if apiKey := os.Getenv(envPrefix + "API_KEY"); apiKey != "" {
			chainConfig.APIKey = apiKey
			config.ChainConfigs[chainName] = chainConfig
		}
	}
	
	// AWS credentials override
	if awsKey := os.Getenv("AWS_ACCESS_KEY"); awsKey != "" {
		config.MetricsExport.AWSAccessKey = awsKey
	}
	
	if awsSecret := os.Getenv("AWS_SECRET_KEY"); awsSecret != "" {
		config.MetricsExport.AWSSecretKey = awsSecret
	}
	
	return config
}

// CreateMultiChainMapping creates a chain config mapping from the configuration
func (c *EnterpriseConfig) CreateMultiChainMapping() map[types.SupportedChain]types.ChainConfig {
	chains := make(map[types.SupportedChain]types.ChainConfig)
	
	for chainName, chainConfig := range c.ChainConfigs {
		if !chainConfig.Enabled {
			continue
		}
		
		supportedChain := types.SupportedChain(chainName)
		chains[supportedChain] = chainConfig.ChainConfig
	}
	
	return chains
}

// CreateDataIntegrityService creates a data integrity service from the configuration
func (c *EnterpriseConfig) CreateDataIntegrityService() (*security.DataIntegrityService, error) {
	validityDuration, err := time.ParseDuration(c.DataIntegrity.SignatureValidity)
	if err != nil {
		validityDuration = 24 * time.Hour // Default to 24 hours
	}
	
	opts := security.VerificationOptions{
		SignatureEnabled:     c.DataIntegrity.SignatureEnabled,
		VerificationRequired: c.DataIntegrity.VerificationRequired,
		SignatureValidity:    validityDuration,
		StrictMode:           c.DataIntegrity.StrictMode,
	}
	
	return security.NewDataIntegrityService(opts)
}
