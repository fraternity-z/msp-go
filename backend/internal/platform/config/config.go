package config

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultAPIPrefix         = "/api/v1"
	defaultJWTSecretKey      = "your-secret-key-change-in-production"
	defaultAdminPassword     = "admin123"
	minProductionDBPassBytes = 16
	minProductionSecretBytes = 32
	maxWechatTemplateIDBytes = 256
)

// Config contains process-level settings loaded from environment variables.
type Config struct {
	AppName     string
	AppVersion  string
	Debug       bool
	Environment string

	Host string
	Port int

	APIV1Prefix            string
	CORSOrigins            []string
	CORSAllowMethods       []string
	CORSAllowHeaders       []string
	RequestTimeout         time.Duration
	ExerciseGenTimeout     time.Duration
	SessionChatTimeout     time.Duration
	ReadHeaderTimeout      time.Duration
	ReadTimeout            time.Duration
	WriteTimeout           time.Duration
	IdleTimeout            time.Duration
	ShutdownTimeout        time.Duration
	MetricsEnabled         bool
	ManagementAllowedCIDRs []string

	UploadsDir string

	PostgresHost        string
	PostgresPort        int
	PostgresUser        string
	PostgresPassword    string
	PostgresDB          string
	DBPoolSize          int
	DBPoolMinConns      int
	DBPoolRecycle       time.Duration
	DBConnectTimeout    time.Duration
	DBStatementTimeout  time.Duration
	DBIdleTxTimeout     time.Duration
	DBHealthCheckPeriod time.Duration

	RedisHost                 string
	RedisPort                 int
	RedisPassword             string
	RedisDB                   int
	RedisMaxConnections       int
	RedisSocketTimeout        time.Duration
	RedisConnectTimeout       time.Duration
	RedisFallbackCacheMaxSize int

	QdrantEnabled            bool
	QdrantURL                string
	QdrantAPIKey             string
	QdrantCollection         string
	QdrantPayloadIndexFields []string
	QdrantTimeout            time.Duration
	QdrantHealthTimeout      time.Duration
	QdrantMaxBatchSize       int
	QdrantWaitForChanges     bool

	JWTSecretKey          string
	JWTAlgorithm          string
	JWTAccessTokenExpire  time.Duration
	JWTRefreshTokenExpire time.Duration

	AdminUsername string
	AdminEmail    string
	AdminPassword string

	LoginMaxAttempts        int
	LoginLockout            time.Duration
	LoginCaptchaTTL         time.Duration
	LoginCaptchaProofTTL    time.Duration
	LoginCaptchaTolerance   int
	LoginCaptchaIssueLimit  int
	LoginCaptchaIssueWindow time.Duration

	LogArchiveAfterDays  int
	LogDeleteAfterDays   int
	LogCleanupEnabled    bool
	LogCleanupInterval   time.Duration
	LogCleanupTimeout    time.Duration
	LogCleanupBatchSize  int
	LogCleanupMaxBatches int
	LogMaxCount          int

	FernetSecretKey string

	XidianIDsBase            string
	XidianEhallBase          string
	XidianUserAgent          string
	XidianHTTPConnectTimeout time.Duration
	XidianHTTPReadTimeout    time.Duration
	XidianChallengeTTL       time.Duration
	XidianHTTPRetryCount     int
	XidianCaptchaWidth       int
	XidianCaptchaHeight      int
	XidianPieceWidth         int
	XidianPieceHeight        int

	WechatOfficialAccountEnabled     bool
	WechatOfficialAccountAppID       string
	WechatOfficialAccountAppSecret   string
	WechatOfficialAccountToken       string
	WechatOfficialAccountAESKey      string
	WechatOfficialAccountMessageMode string
	WechatOfficialAccountName        string
	WechatOfficialAccountHTTPTimeout time.Duration
	WechatMessageRemindersEnabled    bool
	WechatPrivateMessageTemplateID   string
	WechatNoticeTemplateID           string
	WechatQAMessageTemplateID        string

	DailyQuestionPreGenerationEnabled        bool
	DailyQuestionPreGenerationBatchSize      int
	DailyQuestionPreGenerationConcurrency    int
	DailyQuestionPreGenerationBatchInterval  time.Duration
	DailyQuestionPreGenerationStudentTimeout time.Duration

	EinoEnabled       bool
	EinoBaseURL       string
	EinoAPIKey        string
	EinoModel         string
	EinoTimeout       time.Duration
	EinoTemperature   float64
	EinoMaxTokens     int
	EinoMaxIterations int
}

// Load reads the single repository .env file without overwriting process env.
func Load() (Config, error) {
	loadEnvFiles([]string{
		".env",
		filepath.Join("..", ".env"),
	})

	cfg := Config{
		AppName:                   envString("APP_NAME", "高等数学智能学习平台"),
		AppVersion:                envString("APP_VERSION", "0.1.0"),
		Debug:                     envBool("DEBUG", false),
		Environment:               envString("ENVIRONMENT", "development"),
		Host:                      envString("GO_API_HOST", envString("HOST", "0.0.0.0")),
		Port:                      envInt("GO_API_PORT", envInt("PORT", 8000)),
		APIV1Prefix:               cleanPrefix(envString("API_V1_PREFIX", defaultAPIPrefix)),
		CORSOrigins:               envList("CORS_ORIGINS", []string{"http://localhost:3000", "http://localhost:5173"}),
		CORSAllowMethods:          envList("CORS_ALLOW_METHODS", []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}),
		CORSAllowHeaders:          envList("CORS_ALLOW_HEADERS", []string{"Authorization", "Content-Type", "Accept", "Origin", "X-Requested-With", "X-CSRF-Token"}),
		RequestTimeout:            envSeconds("REQUEST_TIMEOUT_DEFAULT", 30*time.Second),
		ExerciseGenTimeout:        envSeconds("EXERCISE_GENERATION_REQUEST_TIMEOUT_SECONDS", 55*time.Second),
		SessionChatTimeout:        envSeconds("SESSION_CHAT_REQUEST_TIMEOUT_SECONDS", 130*time.Second),
		ReadHeaderTimeout:         envSeconds("HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:               envSeconds("HTTP_READ_TIMEOUT", 35*time.Second),
		WriteTimeout:              envSeconds("HTTP_WRITE_TIMEOUT", 310*time.Second),
		IdleTimeout:               envSeconds("HTTP_IDLE_TIMEOUT", 60*time.Second),
		ShutdownTimeout:           envSeconds("HTTP_SHUTDOWN_TIMEOUT", 10*time.Second),
		MetricsEnabled:            envBool("METRICS_ENABLED", true),
		ManagementAllowedCIDRs:    envList("MANAGEMENT_ALLOWED_CIDRS", []string{"127.0.0.1/32", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}),
		UploadsDir:                envString("UPLOADS_DIR", filepath.Join("..", "uploads")),
		PostgresHost:              envString("POSTGRES_HOST", "localhost"),
		PostgresPort:              envInt("POSTGRES_PORT", 5432),
		PostgresUser:              envString("POSTGRES_USER", "postgres"),
		PostgresPassword:          envString("POSTGRES_PASSWORD", ""),
		PostgresDB:                envString("POSTGRES_DB", "math_platform"),
		DBPoolSize:                envInt("DB_POOL_SIZE", 12),
		DBPoolMinConns:            envInt("DB_POOL_MIN_CONNS", 0),
		DBPoolRecycle:             envSeconds("DB_POOL_RECYCLE_SECONDS", 1800*time.Second),
		DBConnectTimeout:          envSeconds("DB_CONNECT_TIMEOUT_SECONDS", 5*time.Second),
		DBStatementTimeout:        envMilliseconds("DB_STATEMENT_TIMEOUT_MS", 30*time.Second),
		DBIdleTxTimeout:           envMilliseconds("DB_IDLE_TX_TIMEOUT_MS", 60*time.Second),
		DBHealthCheckPeriod:       envSeconds("DB_HEALTH_CHECK_PERIOD_SECONDS", 30*time.Second),
		RedisHost:                 envString("REDIS_HOST", "localhost"),
		RedisPort:                 envInt("REDIS_PORT", 6379),
		RedisPassword:             envString("REDIS_PASSWORD", ""),
		RedisDB:                   envInt("REDIS_DB", 0),
		RedisMaxConnections:       envInt("REDIS_MAX_CONNECTIONS", 20),
		RedisSocketTimeout:        envSeconds("REDIS_SOCKET_TIMEOUT_SECONDS", 3*time.Second),
		RedisConnectTimeout:       envSeconds("REDIS_SOCKET_CONNECT_TIMEOUT_SECONDS", 3*time.Second),
		RedisFallbackCacheMaxSize: envInt("REDIS_FALLBACK_CACHE_MAX_SIZE", 500),
		QdrantEnabled:             envBool("QDRANT_ENABLED", false),
		QdrantURL:                 envString("QDRANT_URL", "http://localhost:6333"),
		QdrantAPIKey:              envString("QDRANT_API_KEY", ""),
		QdrantCollection:          envString("QDRANT_COLLECTION", "resource_chunks_dense_default_v1"),
		QdrantPayloadIndexFields:  envList("QDRANT_PAYLOAD_INDEX_FIELDS", []string{"tenant_id", "knowledge_base_id", "resource_id", "generation_id", "visibility"}),
		QdrantTimeout:             envSeconds("QDRANT_TIMEOUT_SECONDS", 5*time.Second),
		QdrantHealthTimeout:       envSeconds("QDRANT_HEALTH_TIMEOUT_SECONDS", 3*time.Second),
		QdrantMaxBatchSize:        envInt("QDRANT_MAX_BATCH_SIZE", 64),
		QdrantWaitForChanges:      envBool("QDRANT_WAIT_FOR_CHANGES", true),
		JWTSecretKey:              envString("JWT_SECRET_KEY", defaultJWTSecretKey),
		JWTAlgorithm:              strings.ToUpper(envString("JWT_ALGORITHM", "HS256")),
		JWTAccessTokenExpire:      time.Duration(envInt("JWT_ACCESS_TOKEN_EXPIRE_MINUTES", 30)) * time.Minute,
		JWTRefreshTokenExpire:     time.Duration(envInt("JWT_REFRESH_TOKEN_EXPIRE_DAYS", 7)) * 24 * time.Hour,
		AdminUsername:             envString("ADMIN_USERNAME", "admin"),
		AdminEmail:                envString("ADMIN_EMAIL", "admin@example.com"),
		AdminPassword:             envString("ADMIN_PASSWORD", defaultAdminPassword),
		LoginMaxAttempts:          envInt("LOGIN_MAX_ATTEMPTS", 5),
		LoginLockout:              time.Duration(envInt("LOGIN_LOCKOUT_MINUTES", 15)) * time.Minute,
		LoginCaptchaTTL:           envSeconds("LOGIN_CAPTCHA_TTL_SECONDS", 2*time.Minute),
		LoginCaptchaProofTTL:      envSeconds("LOGIN_CAPTCHA_PROOF_TTL_SECONDS", 2*time.Minute),
		LoginCaptchaTolerance:     envInt("LOGIN_CAPTCHA_TOLERANCE_PIXELS", 6),
		LoginCaptchaIssueLimit:    envInt("LOGIN_CAPTCHA_ISSUE_LIMIT", 10),
		LoginCaptchaIssueWindow:   envSeconds("LOGIN_CAPTCHA_ISSUE_WINDOW_SECONDS", time.Minute),
		LogArchiveAfterDays:       envInt("LOG_ARCHIVE_AFTER_DAYS", 30),
		LogDeleteAfterDays:        envInt("LOG_DELETE_AFTER_DAYS", 90),
		LogCleanupEnabled:         envBool("LOG_CLEANUP_ENABLED", true),
		LogCleanupInterval:        envSeconds("LOG_CLEANUP_INTERVAL_SECONDS", time.Hour),
		LogCleanupTimeout:         envSeconds("LOG_CLEANUP_TIMEOUT_SECONDS", 30*time.Second),
		LogCleanupBatchSize:       envInt("LOG_CLEANUP_BATCH_SIZE", 500),
		LogCleanupMaxBatches:      envInt("LOG_CLEANUP_MAX_BATCHES", 10),
		LogMaxCount:               envInt("LOG_MAX_COUNT", 100000),
		FernetSecretKey:           envString("FERNET_SECRET_KEY", ""),
		XidianIDsBase:             envString("XIDIAN_IDS_BASE", "https://ids.xidian.edu.cn"),
		XidianEhallBase:           envString("XIDIAN_EHALL_BASE", "https://ehall.xidian.edu.cn"),
		XidianUserAgent: envString("XIDIAN_USER_AGENT",
			"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"),
		XidianHTTPConnectTimeout:                 envSeconds("XIDIAN_HTTP_CONNECT_TIMEOUT", 10*time.Second),
		XidianHTTPReadTimeout:                    envSeconds("XIDIAN_HTTP_READ_TIMEOUT", 30*time.Second),
		XidianChallengeTTL:                       time.Duration(envInt("XIDIAN_CHALLENGE_TTL", 600)) * time.Second,
		XidianHTTPRetryCount:                     envInt("XIDIAN_HTTP_RETRY_COUNT", 2),
		XidianCaptchaWidth:                       envInt("XIDIAN_CAPTCHA_WIDTH", 280),
		XidianCaptchaHeight:                      envInt("XIDIAN_CAPTCHA_HEIGHT", 155),
		XidianPieceWidth:                         envInt("XIDIAN_PIECE_WIDTH", 44),
		XidianPieceHeight:                        envInt("XIDIAN_PIECE_HEIGHT", 155),
		WechatOfficialAccountEnabled:             envBool("WECHAT_OFFICIAL_ACCOUNT_ENABLED", false),
		WechatOfficialAccountAppID:               envString("WECHAT_OFFICIAL_ACCOUNT_APP_ID", ""),
		WechatOfficialAccountAppSecret:           envString("WECHAT_OFFICIAL_ACCOUNT_APP_SECRET", ""),
		WechatOfficialAccountToken:               envString("WECHAT_OFFICIAL_ACCOUNT_TOKEN", ""),
		WechatOfficialAccountAESKey:              envString("WECHAT_OFFICIAL_ACCOUNT_AES_KEY", ""),
		WechatOfficialAccountMessageMode:         strings.ToLower(envString("WECHAT_OFFICIAL_ACCOUNT_MESSAGE_MODE", "plain")),
		WechatOfficialAccountName:                envString("WECHAT_OFFICIAL_ACCOUNT_NAME", "微信公众号"),
		WechatOfficialAccountHTTPTimeout:         envSeconds("WECHAT_OFFICIAL_ACCOUNT_HTTP_TIMEOUT_SECONDS", 10*time.Second),
		WechatMessageRemindersEnabled:            envBool("WECHAT_MESSAGE_REMINDERS_ENABLED", false),
		WechatPrivateMessageTemplateID:           envString("WECHAT_PRIVATE_MESSAGE_TEMPLATE_ID", ""),
		WechatNoticeTemplateID:                   envString("WECHAT_NOTICE_TEMPLATE_ID", ""),
		WechatQAMessageTemplateID:                envString("WECHAT_QA_MESSAGE_TEMPLATE_ID", ""),
		DailyQuestionPreGenerationEnabled:        envBool("DAILY_QUESTION_PREGENERATION_ENABLED", true),
		DailyQuestionPreGenerationBatchSize:      envInt("DAILY_QUESTION_PREGENERATION_BATCH_SIZE", 100),
		DailyQuestionPreGenerationConcurrency:    envInt("DAILY_QUESTION_PREGENERATION_CONCURRENCY", 4),
		DailyQuestionPreGenerationBatchInterval:  envMilliseconds("DAILY_QUESTION_PREGENERATION_BATCH_INTERVAL_MS", time.Second),
		DailyQuestionPreGenerationStudentTimeout: envSeconds("DAILY_QUESTION_PREGENERATION_STUDENT_TIMEOUT_SECONDS", 60*time.Second),
		EinoEnabled:                              envBool("EINO_ENABLED", false),
		EinoBaseURL:                              envString("EINO_BASE_URL", ""),
		EinoAPIKey:                               envString("EINO_API_KEY", ""),
		EinoModel:                                envString("EINO_MODEL", ""),
		EinoTimeout:                              envSeconds("EINO_TIMEOUT_SECONDS", 45*time.Second),
		EinoTemperature:                          envFloat("EINO_TEMPERATURE", 0.3),
		EinoMaxTokens:                            envInt("EINO_MAX_TOKENS", 1200),
		EinoMaxIterations:                        envInt("EINO_MAX_ITERATIONS", 8),
	}

	if cfg.Port <= 0 || cfg.Port > 65535 {
		return Config{}, fmt.Errorf("GO_API_PORT must be between 1 and 65535, got %d", cfg.Port)
	}
	if cfg.ExerciseGenTimeout <= 0 {
		return Config{}, errors.New("EXERCISE_GENERATION_REQUEST_TIMEOUT_SECONDS must be greater than 0")
	}
	if cfg.SessionChatTimeout <= 0 {
		return Config{}, errors.New("SESSION_CHAT_REQUEST_TIMEOUT_SECONDS must be greater than 0")
	}
	if cfg.DBPoolSize <= 0 {
		return Config{}, errors.New("DB_POOL_SIZE must be greater than 0")
	}
	if cfg.DBPoolMinConns < 0 {
		return Config{}, errors.New("DB_POOL_MIN_CONNS must be zero or greater")
	}
	if cfg.DBPoolMinConns > cfg.DBPoolSize {
		return Config{}, errors.New("DB_POOL_MIN_CONNS must not exceed DB_POOL_SIZE")
	}
	if err := validateProductionDatabaseConfig(cfg); err != nil {
		return Config{}, err
	}
	if cfg.RedisMaxConnections <= 0 {
		return Config{}, errors.New("REDIS_MAX_CONNECTIONS must be greater than 0")
	}
	if cfg.RedisFallbackCacheMaxSize <= 0 {
		return Config{}, errors.New("REDIS_FALLBACK_CACHE_MAX_SIZE must be greater than 0")
	}
	if err := validateQdrantConfig(cfg); err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(cfg.UploadsDir) == "" {
		return Config{}, errors.New("UPLOADS_DIR must not be empty")
	}
	if !allowedJWTAlgorithms()[cfg.JWTAlgorithm] {
		return Config{}, fmt.Errorf("JWT_ALGORITHM must be one of HS256, HS384, HS512, got %s", cfg.JWTAlgorithm)
	}
	if strings.TrimSpace(cfg.JWTSecretKey) == "" {
		return Config{}, errors.New("JWT_SECRET_KEY must not be empty")
	}
	if cfg.JWTAccessTokenExpire <= 0 {
		return Config{}, errors.New("JWT_ACCESS_TOKEN_EXPIRE_MINUTES must be greater than 0")
	}
	if cfg.JWTRefreshTokenExpire <= 0 {
		return Config{}, errors.New("JWT_REFRESH_TOKEN_EXPIRE_DAYS must be greater than 0")
	}
	if strings.TrimSpace(cfg.AdminUsername) == "" {
		return Config{}, errors.New("ADMIN_USERNAME must not be empty")
	}
	if strings.TrimSpace(cfg.AdminEmail) == "" {
		return Config{}, errors.New("ADMIN_EMAIL must not be empty")
	}
	if strings.TrimSpace(cfg.AdminPassword) == "" {
		return Config{}, errors.New("ADMIN_PASSWORD must not be empty")
	}
	if err := validateProductionAuthConfig(cfg); err != nil {
		return Config{}, err
	}
	if err := validateCORSConfig(cfg); err != nil {
		return Config{}, err
	}
	if cfg.LoginMaxAttempts <= 0 {
		return Config{}, errors.New("LOGIN_MAX_ATTEMPTS must be greater than 0")
	}
	if cfg.LoginLockout <= 0 {
		return Config{}, errors.New("LOGIN_LOCKOUT_MINUTES must be greater than 0")
	}
	if cfg.LoginCaptchaTTL <= 0 {
		return Config{}, errors.New("LOGIN_CAPTCHA_TTL_SECONDS must be greater than 0")
	}
	if cfg.LoginCaptchaProofTTL <= 0 {
		return Config{}, errors.New("LOGIN_CAPTCHA_PROOF_TTL_SECONDS must be greater than 0")
	}
	if cfg.LoginCaptchaTolerance <= 0 || cfg.LoginCaptchaTolerance >= 48 {
		return Config{}, errors.New("LOGIN_CAPTCHA_TOLERANCE_PIXELS must be between 1 and 47")
	}
	if cfg.LoginCaptchaIssueLimit <= 0 {
		return Config{}, errors.New("LOGIN_CAPTCHA_ISSUE_LIMIT must be greater than 0")
	}
	if cfg.LoginCaptchaIssueWindow <= 0 {
		return Config{}, errors.New("LOGIN_CAPTCHA_ISSUE_WINDOW_SECONDS must be greater than 0")
	}
	if cfg.LogArchiveAfterDays <= 0 {
		return Config{}, errors.New("LOG_ARCHIVE_AFTER_DAYS must be greater than 0")
	}
	if cfg.LogDeleteAfterDays <= 0 {
		return Config{}, errors.New("LOG_DELETE_AFTER_DAYS must be greater than 0")
	}
	if cfg.LogDeleteAfterDays < cfg.LogArchiveAfterDays {
		return Config{}, errors.New("LOG_DELETE_AFTER_DAYS must be greater than or equal to LOG_ARCHIVE_AFTER_DAYS")
	}
	if cfg.LogCleanupInterval <= 0 {
		return Config{}, errors.New("LOG_CLEANUP_INTERVAL_SECONDS must be greater than 0")
	}
	if cfg.LogCleanupTimeout <= 0 {
		return Config{}, errors.New("LOG_CLEANUP_TIMEOUT_SECONDS must be greater than 0")
	}
	if cfg.LogCleanupBatchSize <= 0 {
		return Config{}, errors.New("LOG_CLEANUP_BATCH_SIZE must be greater than 0")
	}
	if cfg.LogCleanupMaxBatches <= 0 {
		return Config{}, errors.New("LOG_CLEANUP_MAX_BATCHES must be greater than 0")
	}
	if cfg.LogMaxCount <= 0 {
		return Config{}, errors.New("LOG_MAX_COUNT must be greater than 0")
	}
	if err := validateXidianConfig(cfg); err != nil {
		return Config{}, err
	}
	if err := validateWechatOfficialAccountConfig(cfg); err != nil {
		return Config{}, err
	}
	if err := validateDailyQuestionPreGenerationConfig(cfg); err != nil {
		return Config{}, err
	}
	if err := validateEinoConfig(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// HTTPAddr returns the TCP address used by the HTTP server.
func (c Config) HTTPAddr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// DatabaseURL returns the PostgreSQL DSN for pgx.
func (c Config) DatabaseURL() string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.PostgresUser, c.PostgresPassword),
		Host:   net.JoinHostPort(c.PostgresHost, strconv.Itoa(c.PostgresPort)),
		Path:   c.PostgresDB,
	}
	return u.String()
}

// RedisAddr returns the host:port address for Redis.
func (c Config) RedisAddr() string {
	return net.JoinHostPort(c.RedisHost, strconv.Itoa(c.RedisPort))
}

// RequiresSharedRefreshSessionStore reports whether refresh sessions must use
// shared Redis state instead of local fallback.
func (c Config) RequiresSharedRefreshSessionStore() bool {
	return isStrictEnvironment(c.Environment)
}

func loadEnvFiles(paths []string) {
	for _, path := range paths {
		loadEnvFile(path)
	}
}

func loadEnvFile(path string) {
	// #nosec G304 -- callers provide only the repository's fixed env-file candidates.
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, trimEnvValue(value)); err != nil {
			continue
		}
	}
}

func trimEnvValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return value
}

func envString(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envFloat(key string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envSeconds(key string, fallback time.Duration) time.Duration {
	return envDuration(key, fallback, time.Second)
}

func envMilliseconds(key string, fallback time.Duration) time.Duration {
	return envDuration(key, fallback, time.Millisecond)
}

func envDuration(key string, fallback time.Duration, unit time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	if strings.ContainsAny(value, "hms") {
		parsed, err := time.ParseDuration(value)
		if err == nil {
			return parsed
		}
	}
	amount, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return time.Duration(amount * float64(unit))
}

func envList(key string, fallback []string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return parseEnvList(value, fallback)
}

func parseEnvList(value string, fallback []string) []string {
	if strings.HasPrefix(value, "[") {
		jsonValues, ok, hasTrailingData := decodeStrictJSONList(value)
		if ok {
			return listOrFallback(cleanEnvListItems(jsonValues), fallback)
		}
		if hasTrailingData {
			return fallback
		}
	}
	return listOrFallback(splitEnvList(value), fallback)
}

func decodeStrictJSONList(value string) ([]string, bool, bool) {
	var target []string
	decoder := json.NewDecoder(strings.NewReader(value))
	if err := decoder.Decode(&target); err != nil {
		return nil, false, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, false, true
	}
	return target, true, false
}

func splitEnvList(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		item := cleanEnvListItem(strings.Trim(strings.TrimSpace(part), `"'[]`))
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func cleanEnvListItems(items []string) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = cleanEnvListItem(item)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func cleanEnvListItem(item string) string {
	return strings.TrimSpace(item)
}

func listOrFallback(values, fallback []string) []string {
	if len(values) == 0 {
		return fallback
	}
	return values
}

func cleanPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return defaultAPIPrefix
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	return strings.TrimRight(prefix, "/")
}

func allowedJWTAlgorithms() map[string]bool {
	return map[string]bool{
		"HS256": true,
		"HS384": true,
		"HS512": true,
	}
}

func validateProductionAuthConfig(cfg Config) error {
	if !isStrictEnvironment(cfg.Environment) {
		return nil
	}
	secret := strings.TrimSpace(cfg.JWTSecretKey)
	if len([]byte(secret)) < minProductionSecretBytes || placeholderSecret(secret) {
		return errors.New("JWT_SECRET_KEY must be a non-placeholder secret with at least 32 bytes outside development")
	}
	adminPassword := strings.TrimSpace(cfg.AdminPassword)
	if adminPassword == defaultAdminPassword || placeholderSecret(adminPassword) {
		return errors.New("ADMIN_PASSWORD must not use the default or placeholder value outside development")
	}
	if !strongConfigPassword(adminPassword) {
		return errors.New("ADMIN_PASSWORD must be 8-72 bytes and include uppercase, lowercase, digit, and special characters outside development")
	}
	return nil
}

func validateProductionDatabaseConfig(cfg Config) error {
	if !isStrictEnvironment(cfg.Environment) {
		return nil
	}
	password := strings.TrimSpace(cfg.PostgresPassword)
	if len([]byte(password)) < minProductionDBPassBytes || placeholderSecret(password) ||
		strings.EqualFold(password, "postgres") || strings.EqualFold(password, "password") ||
		strings.EqualFold(password, strings.TrimSpace(cfg.PostgresUser)) {
		return errors.New("POSTGRES_PASSWORD must be a non-placeholder password with at least 16 bytes outside development")
	}
	return nil
}

func validateCORSConfig(cfg Config) error {
	if !isStrictEnvironment(cfg.Environment) {
		return nil
	}
	for _, origin := range cfg.CORSOrigins {
		if strings.TrimSpace(origin) == "*" {
			return errors.New("CORS_ORIGINS must not contain * outside development")
		}
	}
	return nil
}

func isStrictEnvironment(environment string) bool {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "", "development", "dev", "local", "test":
		return false
	default:
		return true
	}
}

func placeholderSecret(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "change-me", "change-me-in-each-environment", defaultJWTSecretKey, "secret", "test-secret":
		return true
	default:
		return false
	}
}

func strongConfigPassword(password string) bool {
	if len(password) < 8 || len([]byte(password)) > 72 {
		return false
	}
	hasUpper, hasLower, hasDigit, hasSpecial := false, false, false, false
	for _, r := range password {
		switch {
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= '0' && r <= '9':
			hasDigit = true
		default:
			hasSpecial = true
		}
	}
	return hasUpper && hasLower && hasDigit && hasSpecial
}

func requireConfigValues(name string, values map[string]string) error {
	missing := make([]string, 0)
	for key, value := range values {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("%s storage config missing: %s", name, strings.Join(missing, ", "))
	}
	return nil
}

func validateXidianConfig(cfg Config) error {
	if cfg.XidianHTTPConnectTimeout <= 0 {
		return errors.New("XIDIAN_HTTP_CONNECT_TIMEOUT must be greater than 0")
	}
	if cfg.XidianHTTPReadTimeout <= 0 {
		return errors.New("XIDIAN_HTTP_READ_TIMEOUT must be greater than 0")
	}
	if cfg.XidianChallengeTTL <= 0 {
		return errors.New("XIDIAN_CHALLENGE_TTL must be greater than 0")
	}
	if cfg.XidianHTTPRetryCount < 0 {
		return errors.New("XIDIAN_HTTP_RETRY_COUNT must be zero or greater")
	}
	if cfg.XidianCaptchaWidth <= 0 || cfg.XidianCaptchaHeight <= 0 || cfg.XidianPieceWidth <= 0 || cfg.XidianPieceHeight <= 0 {
		return errors.New("xidian captcha dimensions must be greater than 0")
	}
	return requireConfigValues("Xidian", map[string]string{
		"XIDIAN_IDS_BASE":   cfg.XidianIDsBase,
		"XIDIAN_EHALL_BASE": cfg.XidianEhallBase,
		"XIDIAN_USER_AGENT": cfg.XidianUserAgent,
	})
}

func validateWechatOfficialAccountConfig(cfg Config) error {
	if cfg.WechatMessageRemindersEnabled && !cfg.WechatOfficialAccountEnabled {
		return errors.New("WECHAT_MESSAGE_REMINDERS_ENABLED requires WECHAT_OFFICIAL_ACCOUNT_ENABLED=true")
	}
	if cfg.WechatMessageRemindersEnabled {
		templates := map[string]string{
			"WECHAT_NOTICE_TEMPLATE_ID":          cfg.WechatNoticeTemplateID,
			"WECHAT_PRIVATE_MESSAGE_TEMPLATE_ID": cfg.WechatPrivateMessageTemplateID,
			"WECHAT_QA_MESSAGE_TEMPLATE_ID":      cfg.WechatQAMessageTemplateID,
		}
		missing := make([]string, 0)
		for key, value := range templates {
			if strings.TrimSpace(value) == "" {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return fmt.Errorf("WeChat reminder config missing: %s", strings.Join(missing, ", "))
		}
		for key, value := range templates {
			if len(value) > maxWechatTemplateIDBytes || !utf8.ValidString(value) || strings.ContainsAny(value, "\t\r\n") {
				return fmt.Errorf("%s must be a valid WeChat template ID of at most %d bytes", key, maxWechatTemplateIDBytes)
			}
		}
	}
	if !cfg.WechatOfficialAccountEnabled {
		return nil
	}
	switch cfg.WechatOfficialAccountMessageMode {
	case "plain", "compatible", "safe":
	default:
		return fmt.Errorf("WECHAT_OFFICIAL_ACCOUNT_MESSAGE_MODE must be one of plain, compatible, safe, got %s", cfg.WechatOfficialAccountMessageMode)
	}
	values := map[string]string{
		"WECHAT_OFFICIAL_ACCOUNT_APP_ID":     cfg.WechatOfficialAccountAppID,
		"WECHAT_OFFICIAL_ACCOUNT_APP_SECRET": cfg.WechatOfficialAccountAppSecret,
		"WECHAT_OFFICIAL_ACCOUNT_TOKEN":      cfg.WechatOfficialAccountToken,
		"WECHAT_OFFICIAL_ACCOUNT_NAME":       cfg.WechatOfficialAccountName,
	}
	missing := make([]string, 0)
	for key, value := range values {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("WeChat Official Account config missing: %s", strings.Join(missing, ", "))
	}
	if !asciiAlphaNumeric(cfg.WechatOfficialAccountToken) || len(cfg.WechatOfficialAccountToken) < 3 || len(cfg.WechatOfficialAccountToken) > 32 {
		return errors.New("WECHAT_OFFICIAL_ACCOUNT_TOKEN must contain 3 to 32 ASCII letters or digits")
	}
	if cfg.WechatOfficialAccountHTTPTimeout < time.Second || cfg.WechatOfficialAccountHTTPTimeout > 60*time.Second {
		return errors.New("WECHAT_OFFICIAL_ACCOUNT_HTTP_TIMEOUT_SECONDS must be between 1 and 60 seconds")
	}
	needsAES := cfg.WechatOfficialAccountMessageMode == "compatible" || cfg.WechatOfficialAccountMessageMode == "safe"
	if needsAES && strings.TrimSpace(cfg.WechatOfficialAccountAESKey) == "" {
		return errors.New("WECHAT_OFFICIAL_ACCOUNT_AES_KEY must not be empty in compatible or safe message mode")
	}
	if cfg.WechatOfficialAccountAESKey != "" {
		if len(cfg.WechatOfficialAccountAESKey) != 43 {
			return errors.New("WECHAT_OFFICIAL_ACCOUNT_AES_KEY must contain exactly 43 Base64 characters")
		}
		decoded, err := base64.StdEncoding.DecodeString(cfg.WechatOfficialAccountAESKey + "=")
		if err != nil || len(decoded) != 32 {
			return errors.New("WECHAT_OFFICIAL_ACCOUNT_AES_KEY must decode to 32 bytes")
		}
	}
	return nil
}

func asciiAlphaNumeric(value string) bool {
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func validateDailyQuestionPreGenerationConfig(cfg Config) error {
	if cfg.DailyQuestionPreGenerationBatchSize <= 0 {
		return errors.New("DAILY_QUESTION_PREGENERATION_BATCH_SIZE must be greater than 0")
	}
	if cfg.DailyQuestionPreGenerationConcurrency <= 0 {
		return errors.New("DAILY_QUESTION_PREGENERATION_CONCURRENCY must be greater than 0")
	}
	if cfg.DailyQuestionPreGenerationConcurrency > cfg.DailyQuestionPreGenerationBatchSize {
		return errors.New("DAILY_QUESTION_PREGENERATION_CONCURRENCY must not exceed DAILY_QUESTION_PREGENERATION_BATCH_SIZE")
	}
	if cfg.DailyQuestionPreGenerationBatchInterval < 0 {
		return errors.New("DAILY_QUESTION_PREGENERATION_BATCH_INTERVAL_MS must be zero or greater")
	}
	if cfg.DailyQuestionPreGenerationStudentTimeout <= 0 {
		return errors.New("DAILY_QUESTION_PREGENERATION_STUDENT_TIMEOUT_SECONDS must be greater than 0")
	}
	if cfg.DailyQuestionPreGenerationStudentTimeout >= 2*time.Minute {
		return errors.New("DAILY_QUESTION_PREGENERATION_STUDENT_TIMEOUT_SECONDS must be less than 120 seconds")
	}
	return nil
}

func validateEinoConfig(cfg Config) error {
	if !cfg.EinoEnabled {
		return nil
	}
	if strings.TrimSpace(cfg.EinoAPIKey) == "" {
		return errors.New("EINO_API_KEY must not be empty when EINO_ENABLED=true")
	}
	if strings.TrimSpace(cfg.EinoModel) == "" {
		return errors.New("EINO_MODEL must not be empty when EINO_ENABLED=true")
	}
	if cfg.EinoTimeout <= 0 {
		return errors.New("EINO_TIMEOUT_SECONDS must be greater than 0 when EINO_ENABLED=true")
	}
	if cfg.EinoTemperature < 0 || cfg.EinoTemperature > 2 {
		return errors.New("EINO_TEMPERATURE must be between 0 and 2 when EINO_ENABLED=true")
	}
	if cfg.EinoMaxTokens < 0 {
		return errors.New("EINO_MAX_TOKENS must be zero or greater when EINO_ENABLED=true")
	}
	if cfg.EinoMaxIterations <= 0 {
		return errors.New("EINO_MAX_ITERATIONS must be greater than 0 when EINO_ENABLED=true")
	}
	return nil
}

func validateQdrantConfig(cfg Config) error {
	if !cfg.QdrantEnabled {
		return nil
	}
	parsed, err := url.Parse(strings.TrimSpace(cfg.QdrantURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("QDRANT_URL must include an http or https scheme and host when QDRANT_ENABLED=true")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("QDRANT_URL must not contain credentials, query, or fragment when QDRANT_ENABLED=true")
	}
	if !validQdrantName(cfg.QdrantCollection) {
		return errors.New("QDRANT_COLLECTION must use 1 to 255 ASCII letters, digits, dots, underscores, or hyphens")
	}
	if cfg.QdrantTimeout <= 0 {
		return errors.New("QDRANT_TIMEOUT_SECONDS must be greater than 0 when QDRANT_ENABLED=true")
	}
	if cfg.QdrantHealthTimeout <= 0 {
		return errors.New("QDRANT_HEALTH_TIMEOUT_SECONDS must be greater than 0 when QDRANT_ENABLED=true")
	}
	if cfg.QdrantMaxBatchSize <= 0 || cfg.QdrantMaxBatchSize > 10000 {
		return errors.New("QDRANT_MAX_BATCH_SIZE must be between 1 and 10000 when QDRANT_ENABLED=true")
	}
	for _, field := range cfg.QdrantPayloadIndexFields {
		if !validQdrantField(field) {
			return errors.New("QDRANT_PAYLOAD_INDEX_FIELDS contains an invalid field name")
		}
	}
	if isStrictEnvironment(cfg.Environment) && strings.TrimSpace(cfg.QdrantAPIKey) == "" {
		return errors.New("QDRANT_API_KEY must not be empty when QDRANT_ENABLED=true outside development")
	}
	return nil
}

func validQdrantName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." || len(value) > 255 {
		return false
	}
	for index, char := range value {
		if index == 0 && !asciiQdrantAlphaNumeric(char) {
			return false
		}
		if !asciiQdrantAlphaNumeric(char) && char != '.' && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

func validQdrantField(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." || len(value) > 255 {
		return false
	}
	for _, char := range value {
		if !asciiQdrantAlphaNumeric(char) && char != '.' && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

func asciiQdrantAlphaNumeric(char rune) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')
}
