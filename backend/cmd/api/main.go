package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	emailadapter "mathstudy/backend/internal/adapter/email"
	adminaiconfighthttp "mathstudy/backend/internal/adapter/http/adminaiconfig"
	adminairiskhttp "mathstudy/backend/internal/adapter/http/adminairisk"
	adminemailhttp "mathstudy/backend/internal/adapter/http/adminemail"
	admininboxhttp "mathstudy/backend/internal/adapter/http/admininbox"
	adminsettingshttp "mathstudy/backend/internal/adapter/http/adminsettings"
	adminstatshttp "mathstudy/backend/internal/adapter/http/adminstats"
	adminstoragehttp "mathstudy/backend/internal/adapter/http/adminstorage"
	adminuserhttp "mathstudy/backend/internal/adapter/http/adminuser"
	announcementhttp "mathstudy/backend/internal/adapter/http/announcement"
	authhttp "mathstudy/backend/internal/adapter/http/auth"
	classroomhttp "mathstudy/backend/internal/adapter/http/classroom"
	conversationhttp "mathstudy/backend/internal/adapter/http/conversation"
	dailyquestionhttp "mathstudy/backend/internal/adapter/http/dailyquestion"
	exercisehttp "mathstudy/backend/internal/adapter/http/exercise"
	forumhttp "mathstudy/backend/internal/adapter/http/forum"
	knowledgehttp "mathstudy/backend/internal/adapter/http/knowledge"
	messagecenterhttp "mathstudy/backend/internal/adapter/http/messagecenter"
	mistakehttp "mathstudy/backend/internal/adapter/http/mistake"
	noticehttp "mathstudy/backend/internal/adapter/http/notice"
	openaihttp "mathstudy/backend/internal/adapter/http/openai"
	portraithttp "mathstudy/backend/internal/adapter/http/portrait"
	progresshttp "mathstudy/backend/internal/adapter/http/progress"
	qathreadhttp "mathstudy/backend/internal/adapter/http/qathread"
	questionhttp "mathstudy/backend/internal/adapter/http/question"
	resourcehttp "mathstudy/backend/internal/adapter/http/resource"
	securityloghttp "mathstudy/backend/internal/adapter/http/securitylog"
	sessionhttp "mathstudy/backend/internal/adapter/http/session"
	teacherhttp "mathstudy/backend/internal/adapter/http/teacher"
	uploadhttp "mathstudy/backend/internal/adapter/http/upload"
	wechathttp "mathstudy/backend/internal/adapter/http/wechat"
	xidianhttp "mathstudy/backend/internal/adapter/http/xidian"
	einoagent "mathstudy/backend/internal/adapter/llm/einoagent"
	moderationadapter "mathstudy/backend/internal/adapter/llm/moderation"
	openaicompatadapter "mathstudy/backend/internal/adapter/llm/openaicompat"
	adapterpostgres "mathstudy/backend/internal/adapter/postgres"
	qdrantadapter "mathstudy/backend/internal/adapter/qdrant"
	adapterredis "mathstudy/backend/internal/adapter/redis"
	storageadapter "mathstudy/backend/internal/adapter/storage"
	adminaiconfigapp "mathstudy/backend/internal/application/adminaiconfig"
	admininboxapp "mathstudy/backend/internal/application/admininbox"
	adminsettingsapp "mathstudy/backend/internal/application/adminsettings"
	adminstatsapp "mathstudy/backend/internal/application/adminstats"
	adminstorageapp "mathstudy/backend/internal/application/adminstorage"
	adminuserapp "mathstudy/backend/internal/application/adminuser"
	airiskapp "mathstudy/backend/internal/application/airisk"
	announcementapp "mathstudy/backend/internal/application/announcement"
	answerocrapp "mathstudy/backend/internal/application/answerocr"
	authapp "mathstudy/backend/internal/application/auth"
	classroomapp "mathstudy/backend/internal/application/classroom"
	conversationapp "mathstudy/backend/internal/application/conversation"
	dailyquestionapp "mathstudy/backend/internal/application/dailyquestion"
	emailapp "mathstudy/backend/internal/application/email"
	exerciseapp "mathstudy/backend/internal/application/exercise"
	forumapp "mathstudy/backend/internal/application/forum"
	knowledgeapp "mathstudy/backend/internal/application/knowledge"
	messagecenterapp "mathstudy/backend/internal/application/messagecenter"
	mistakeapp "mathstudy/backend/internal/application/mistake"
	noticeapp "mathstudy/backend/internal/application/notice"
	portraitapp "mathstudy/backend/internal/application/portrait"
	progressapp "mathstudy/backend/internal/application/progress"
	qathreadapp "mathstudy/backend/internal/application/qathread"
	questionapp "mathstudy/backend/internal/application/question"
	resourceapp "mathstudy/backend/internal/application/resource"
	securitylogapp "mathstudy/backend/internal/application/securitylog"
	sessionapp "mathstudy/backend/internal/application/session"
	teacherapp "mathstudy/backend/internal/application/teacher"
	uploadapp "mathstudy/backend/internal/application/upload"
	wechatapp "mathstudy/backend/internal/application/wechat"
	wechatreminder "mathstudy/backend/internal/application/wechatreminder"
	xidianapp "mathstudy/backend/internal/application/xidian"
	wechatintegration "mathstudy/backend/internal/integration/wechat"
	xidianintegration "mathstudy/backend/internal/integration/xidian"
	"mathstudy/backend/internal/platform/config"
	"mathstudy/backend/internal/platform/health"
	"mathstudy/backend/internal/platform/httpserver"
	"mathstudy/backend/internal/platform/metrics"
	"mathstudy/backend/internal/platform/outbound"
	platformpostgres "mathstudy/backend/internal/platform/postgres"
	"mathstudy/backend/internal/platform/ratelimit"
	platformredis "mathstudy/backend/internal/platform/redis"
	"mathstudy/backend/internal/platform/secret"
)

const (
	messageCenterWriteRateLimitMax  = 60
	messageCenterSearchRateLimitMax = 30
	messageCenterRateLimitWindow    = time.Minute
	dailyQuestionGenerationLimitMax = 10
	dailyQuestionGenerationWindow   = time.Minute
	openAIResponsesRateLimitMax     = 30
	openAIResponsesRateLimitWindow  = time.Minute
)

func main() {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	logger := newLogger(cfg)
	slog.SetDefault(logger)

	dbPool, err := platformpostgres.NewPool(ctx, cfg)
	if err != nil {
		logger.Error("configure postgres pool", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	redisClient := platformredis.NewClient(cfg)
	defer func() {
		if err := redisClient.Close(); err != nil {
			logger.Warn("close redis client", "error", err)
		}
	}()
	if err := requireSharedRedis(ctx, cfg, redisClient); err != nil {
		logger.Error("redis is required for configured shared state", "error", err)
		os.Exit(1)
	}
	var qdrantClient *qdrantadapter.Client
	var qdrantPinger health.Pinger
	if cfg.QdrantEnabled {
		qdrantClient, err = qdrantadapter.New(qdrantadapter.Config{
			BaseURL:        cfg.QdrantURL,
			APIKey:         cfg.QdrantAPIKey,
			Collection:     cfg.QdrantCollection,
			Timeout:        cfg.QdrantTimeout,
			HealthTimeout:  cfg.QdrantHealthTimeout,
			MaxBatchSize:   cfg.QdrantMaxBatchSize,
			WaitForChanges: cfg.QdrantWaitForChanges,
			PayloadIndexes: cfg.QdrantPayloadIndexFields,
		})
		if err != nil {
			logger.Error("configure qdrant vector index", "error", err)
			os.Exit(1)
		}
		qdrantPinger = qdrantClient
		logger.Info("qdrant vector capability enabled")
	}
	store := metrics.NewStore(cfg.AppVersion, cfg.Environment)
	store.SetRuntimeStatsProvider(func() metrics.RuntimeStats {
		postgresStats := dbPool.Stat()
		redisStats := redisClient.PoolStats()
		return metrics.RuntimeStats{
			Postgres: metrics.PostgresPoolStats{
				MaxConnections:          int64(postgresStats.MaxConns()),
				TotalConnections:        int64(postgresStats.TotalConns()),
				AcquiredConnections:     int64(postgresStats.AcquiredConns()),
				IdleConnections:         int64(postgresStats.IdleConns()),
				ConstructingConnections: int64(postgresStats.ConstructingConns()),
				AcquireCount:            postgresStats.AcquireCount(),
				EmptyAcquireCount:       postgresStats.EmptyAcquireCount(),
				CanceledAcquireCount:    postgresStats.CanceledAcquireCount(),
				AcquireDuration:         postgresStats.AcquireDuration(),
				EmptyAcquireWaitTime:    postgresStats.EmptyAcquireWaitTime(),
			},
			Redis: metrics.RedisPoolStats{
				MaxConnections:   int64(cfg.RedisMaxConnections),
				TotalConnections: int64(redisStats.TotalConns),
				IdleConnections:  int64(redisStats.IdleConns),
				StaleConnections: int64(redisStats.StaleConns),
				Hits:             uint64(redisStats.Hits),
				Misses:           uint64(redisStats.Misses),
				Timeouts:         uint64(redisStats.Timeouts),
				WaitCount:        uint64(redisStats.WaitCount),
				Unusable:         uint64(redisStats.Unusable),
				WaitDuration:     time.Duration(redisStats.WaitDurationNs),
			},
		}
	})

	userRepo, err := adapterpostgres.NewUserRepository(dbPool)
	if err != nil {
		logger.Error("configure user repository", "error", err)
		os.Exit(1)
	}
	tokenService, err := authapp.NewTokenService(
		cfg.JWTSecretKey,
		cfg.JWTAlgorithm,
		cfg.JWTAccessTokenExpire,
		cfg.JWTRefreshTokenExpire,
	)
	if err != nil {
		logger.Error("configure token service", "error", err)
		os.Exit(1)
	}
	loginLimiter := authapp.NewLoginLimiter(redisClient, cfg.LoginMaxAttempts, cfg.LoginLockout, logger)
	captchaManager, err := authapp.NewSliderCaptchaManager(redisClient, logger, authapp.SliderCaptchaConfig{
		ChallengeTTL: cfg.LoginCaptchaTTL,
		ProofTTL:     cfg.LoginCaptchaProofTTL,
		IssueWindow:  cfg.LoginCaptchaIssueWindow,
		Tolerance:    cfg.LoginCaptchaTolerance,
		IssueLimit:   cfg.LoginCaptchaIssueLimit,
		MaxLocalSize: cfg.RedisFallbackCacheMaxSize,
		Strict:       cfg.RequiresSharedRefreshSessionStore(),
	})
	if err != nil {
		logger.Error("configure login captcha", "error", err)
		os.Exit(1)
	}
	refreshSessions := authapp.NewRefreshSessionStore(
		redisClient,
		logger,
		authapp.WithStrictRefreshSessions(cfg.RequiresSharedRefreshSessionStore()),
		authapp.WithMaxLocalRefreshSessions(cfg.RedisFallbackCacheMaxSize),
	)
	authService, err := authapp.NewService(
		userRepo,
		userRepo,
		userRepo,
		tokenService,
		loginLimiter,
		logger,
		authapp.WithRefreshSessionStore(refreshSessions),
	)
	if err != nil {
		logger.Error("configure auth service", "error", err)
		os.Exit(1)
	}
	if _, err := authService.InitAdmin(ctx, cfg.AdminUsername, cfg.AdminEmail, cfg.AdminPassword); err != nil {
		logger.Error("initialize admin account", "error", err)
		os.Exit(1)
	}
	authHandler, err := authhttp.NewHandler(cfg, logger, authService, captchaManager)
	if err != nil {
		logger.Error("configure auth handler", "error", err)
		os.Exit(1)
	}
	aiRiskRepo, err := adapterpostgres.NewAIRiskRepository(dbPool)
	if err != nil {
		logger.Error("configure AI risk repository", "error", err)
		os.Exit(1)
	}
	aiRiskSlots, err := adapterredis.NewAIRiskSlotStore(redisClient)
	if err != nil {
		logger.Error("configure AI risk slot store", "error", err)
		os.Exit(1)
	}
	progressRepo, err := adapterpostgres.NewProgressRepository(dbPool)
	if err != nil {
		logger.Error("configure progress repository", "error", err)
		os.Exit(1)
	}
	progressService, err := progressapp.NewService(progressRepo)
	if err != nil {
		logger.Error("configure progress service", "error", err)
		os.Exit(1)
	}
	progressHandler, err := progresshttp.NewHandler(logger, progressService, authService)
	if err != nil {
		logger.Error("configure progress handler", "error", err)
		os.Exit(1)
	}
	fernetKey := cfg.FernetSecretKey
	if fernetKey == "" {
		logger.Warn("FERNET_SECRET_KEY is not configured; using an ephemeral key for local secret encryption")
		fernetKey, err = secret.GenerateFernetKey()
		if err != nil {
			logger.Error("generate ephemeral fernet key", "error", err)
			os.Exit(1)
		}
	}
	appCipher, err := secret.NewFernet(fernetKey)
	if err != nil {
		logger.Error("configure fernet cipher", "error", err)
		os.Exit(1)
	}
	adminAIConfigRepo, err := adapterpostgres.NewAdminAIConfigRepository(dbPool)
	if err != nil {
		logger.Error("configure admin AI config repository", "error", err)
		os.Exit(1)
	}
	providerBaseHTTPClient := outbound.NewPublicHTTPSClient(20 * time.Second)
	if transport, ok := providerBaseHTTPClient.Transport.(*http.Transport); ok {
		transport.MaxIdleConnsPerHost = 20
	}
	providerHTTPClient := openaicompatadapter.WrapClient(providerBaseHTTPClient)
	adminAIConfigService, err := adminaiconfigapp.NewService(adminAIConfigRepo, appCipher, providerHTTPClient)
	if err != nil {
		logger.Error("configure admin AI config service", "error", err)
		os.Exit(1)
	}
	responsesDispatcher, err := openaicompatadapter.NewResponsesDispatcher(adminAIConfigService, providerBaseHTTPClient)
	if err != nil {
		logger.Error("configure OpenAI Responses dispatcher", "error", err)
		os.Exit(1)
	}
	responsesLimiter, err := ratelimit.New(
		redisClient,
		"msp:openai_responses",
		openAIResponsesRateLimitMax,
		openAIResponsesRateLimitWindow,
		cfg.RedisFallbackCacheMaxSize,
		logger,
	)
	if err != nil {
		logger.Error("configure OpenAI Responses rate limit", "error", err)
		os.Exit(1)
	}
	adminAIConfigHandler, err := adminaiconfighthttp.NewHandler(logger, adminAIConfigService, authService)
	if err != nil {
		logger.Error("configure admin AI config handler", "error", err)
		os.Exit(1)
	}
	contentReviewer, err := moderationadapter.NewReviewer(adminAIConfigService)
	if err != nil {
		logger.Error("configure AI content reviewer", "error", err)
		os.Exit(1)
	}
	aiRiskService, err := airiskapp.NewService(aiRiskRepo, aiRiskSlots, airiskapp.WithContentReviewer(contentReviewer))
	if err != nil {
		logger.Error("configure AI risk service", "error", err)
		os.Exit(1)
	}
	openAIHandler, err := openaihttp.NewHandler(
		logger,
		responsesDispatcher,
		authService,
		aiRiskService,
		responsesLimiter,
		store,
	)
	if err != nil {
		logger.Error("configure OpenAI Responses handler", "error", err)
		os.Exit(1)
	}
	aiRiskHandler, err := adminairiskhttp.NewHandler(logger, aiRiskService, authService)
	if err != nil {
		logger.Error("configure AI risk handler", "error", err)
		os.Exit(1)
	}
	portraitRepo, err := adapterpostgres.NewPortraitRepository(dbPool)
	if err != nil {
		logger.Error("configure portrait repository", "error", err)
		os.Exit(1)
	}
	einoConfig := defaultEinoConfig(cfg)
	portraitGenerator := einoagent.NewConfigurablePortraitGenerator(adminAIConfigService, einoConfig)
	portraitService, err := portraitapp.NewService(portraitRepo, portraitapp.WithGenerator(portraitGenerator))
	if err != nil {
		logger.Error("configure portrait service", "error", err)
		os.Exit(1)
	}
	portraitHandler, err := portraithttp.NewHandler(logger, portraitService, authService, portraithttp.WithAIRequestGuard(aiRiskService))
	if err != nil {
		logger.Error("configure portrait handler", "error", err)
		os.Exit(1)
	}
	uploadStorage := storageadapter.NewRuntimeManager(cfg.UploadsDir)
	exerciseRepo, err := adapterpostgres.NewExerciseRepository(dbPool)
	if err != nil {
		logger.Error("configure exercise repository", "error", err)
		os.Exit(1)
	}
	mathSolver := einoagent.NewConfigurableMathSolver(adminAIConfigService, einoConfig)
	answerOCRRecognizer := einoagent.NewConfigurableAnswerOCR(adminAIConfigService, einoConfig)
	answerOCRService, err := answerocrapp.NewService(uploadStorage, answerOCRRecognizer)
	if err != nil {
		logger.Error("configure answer OCR service", "error", err)
		os.Exit(1)
	}
	diagnostician := einoagent.NewConfigurableDiagnostician(adminAIConfigService, einoConfig)
	questionGenerator := einoagent.NewConfigurableQuestionGenerator(adminAIConfigService, einoConfig)
	exerciseService, err := exerciseapp.NewService(
		exerciseRepo,
		exerciseapp.SolverAnswerChecker{Solver: mathSolver},
		exerciseapp.WithAnswerOCR(answerOCRService),
		exerciseapp.WithSolutionSolver(mathSolver),
		exerciseapp.WithDiagnostician(diagnostician),
		exerciseapp.WithQuestionGenerator(questionGenerator),
	)
	if err != nil {
		logger.Error("configure exercise service", "error", err)
		os.Exit(1)
	}
	mistakeRepo, err := adapterpostgres.NewMistakeRepository(dbPool)
	if err != nil {
		logger.Error("configure mistake repository", "error", err)
		os.Exit(1)
	}
	mistakeService, err := mistakeapp.NewService(mistakeRepo, exerciseService)
	if err != nil {
		logger.Error("configure mistake service", "error", err)
		os.Exit(1)
	}
	mistakeHandler, err := mistakehttp.NewHandler(logger, mistakeService, authService)
	if err != nil {
		logger.Error("configure mistake handler", "error", err)
		os.Exit(1)
	}
	exerciseHandler, err := exercisehttp.NewHandler(
		logger,
		exerciseService,
		authService,
		exercisehttp.WithRedisRateLimit(redisClient, cfg.RedisFallbackCacheMaxSize),
		exercisehttp.WithAIRequestGuard(aiRiskService),
	)
	if err != nil {
		logger.Error("configure exercise handler", "error", err)
		os.Exit(1)
	}
	wechatReminderEnqueuer, err := adapterpostgres.NewWechatReminderEnqueuer(
		cfg.WechatMessageRemindersEnabled,
		cfg.WechatOfficialAccountAppID,
	)
	if err != nil {
		logger.Error("configure wechat reminder enqueuer", "error", err)
		os.Exit(1)
	}
	dailyQuestionRepo, err := adapterpostgres.NewDailyQuestionRepository(dbPool, wechatReminderEnqueuer)
	if err != nil {
		logger.Error("configure daily question repository", "error", err)
		os.Exit(1)
	}
	dailyQuestionGenerationLimiter, err := ratelimit.New(
		redisClient,
		"msp:exercise_generation",
		dailyQuestionGenerationLimitMax,
		dailyQuestionGenerationWindow,
		cfg.RedisFallbackCacheMaxSize,
		logger,
	)
	if err != nil {
		logger.Error("configure daily question generation rate limit", "error", err)
		os.Exit(1)
	}
	dailyQuestionService, err := dailyquestionapp.NewService(
		dailyQuestionRepo,
		exerciseService,
		dailyquestionapp.WithAIRequestGuard(aiRiskService),
		dailyquestionapp.WithGenerationLimiter(dailyQuestionGenerationLimiter),
		dailyquestionapp.WithWechatRemindersEnabled(cfg.WechatMessageRemindersEnabled),
	)
	if err != nil {
		logger.Error("configure daily question service", "error", err)
		os.Exit(1)
	}
	dailyQuestionWorker, err := dailyquestionapp.NewWorker(
		dailyQuestionRepo,
		dailyQuestionService,
		logger,
		dailyquestionapp.WorkerConfig{
			BatchSize:      cfg.DailyQuestionPreGenerationBatchSize,
			Concurrency:    cfg.DailyQuestionPreGenerationConcurrency,
			BatchInterval:  cfg.DailyQuestionPreGenerationBatchInterval,
			StudentTimeout: cfg.DailyQuestionPreGenerationStudentTimeout,
		},
	)
	if err != nil {
		logger.Error("configure daily question worker", "error", err)
		os.Exit(1)
	}
	dailyQuestionReminderWorker, err := dailyquestionapp.NewReminderWorker(dailyQuestionService, logger)
	if err != nil {
		logger.Error("configure daily question reminder worker", "error", err)
		os.Exit(1)
	}
	dailyQuestionHandler, err := dailyquestionhttp.NewHandler(
		logger,
		dailyQuestionService,
		authService,
	)
	if err != nil {
		logger.Error("configure daily question handler", "error", err)
		os.Exit(1)
	}
	sessionRepo, err := adapterpostgres.NewSessionRepository(dbPool)
	if err != nil {
		logger.Error("configure session repository", "error", err)
		os.Exit(1)
	}
	tutorAgent := einoagent.NewConfigurableTutorAgent(adminAIConfigService, einoConfig)
	sessionService, err := sessionapp.NewService(
		sessionRepo,
		sessionapp.WithChatAgent(tutorAgent),
		sessionapp.WithAIRequestGuard(aiRiskService),
		sessionapp.WithLogger(logger),
	)
	if err != nil {
		logger.Error("configure session service", "error", err)
		os.Exit(1)
	}
	sessionHandler, err := sessionhttp.NewHandler(logger, sessionService, authService)
	if err != nil {
		logger.Error("configure session handler", "error", err)
		os.Exit(1)
	}
	resourceRepo, err := adapterpostgres.NewResourceRepository(dbPool)
	if err != nil {
		logger.Error("configure resource repository", "error", err)
		os.Exit(1)
	}
	resourceService, err := resourceapp.NewService(resourceRepo)
	if err != nil {
		logger.Error("configure resource service", "error", err)
		os.Exit(1)
	}
	resourceHandler, err := resourcehttp.NewHandler(logger, resourceService, authService)
	if err != nil {
		logger.Error("configure resource handler", "error", err)
		os.Exit(1)
	}
	questionRepo, err := adapterpostgres.NewQuestionRepository(dbPool)
	if err != nil {
		logger.Error("configure question repository", "error", err)
		os.Exit(1)
	}
	questionParser := einoagent.NewConfigurableQuestionParser(adminAIConfigService, einoConfig)
	questionService, err := questionapp.NewService(questionRepo, questionapp.WithParser(questionParser))
	if err != nil {
		logger.Error("configure question service", "error", err)
		os.Exit(1)
	}
	questionHandler, err := questionhttp.NewHandler(logger, questionService, authService)
	if err != nil {
		logger.Error("configure question handler", "error", err)
		os.Exit(1)
	}
	classRepo, err := adapterpostgres.NewClassRepository(dbPool)
	if err != nil {
		logger.Error("configure class repository", "error", err)
		os.Exit(1)
	}
	classService, err := classroomapp.NewService(classRepo)
	if err != nil {
		logger.Error("configure class service", "error", err)
		os.Exit(1)
	}
	classHandler, err := classroomhttp.NewHandler(logger, classService, authService)
	if err != nil {
		logger.Error("configure class handler", "error", err)
		os.Exit(1)
	}
	teacherRepo, err := adapterpostgres.NewTeacherRepository(dbPool)
	if err != nil {
		logger.Error("configure teacher repository", "error", err)
		os.Exit(1)
	}
	teacherService, err := teacherapp.NewService(teacherRepo)
	if err != nil {
		logger.Error("configure teacher service", "error", err)
		os.Exit(1)
	}
	teacherHandler, err := teacherhttp.NewHandler(logger, teacherService, authService)
	if err != nil {
		logger.Error("configure teacher handler", "error", err)
		os.Exit(1)
	}
	messageCenterWriteLimiter, err := ratelimit.New(
		redisClient,
		"msp:message_center:write",
		messageCenterWriteRateLimitMax,
		messageCenterRateLimitWindow,
		cfg.RedisFallbackCacheMaxSize,
		logger,
	)
	if err != nil {
		logger.Error("configure message center write rate limit", "error", err)
		os.Exit(1)
	}
	messageCenterSearchLimiter, err := ratelimit.New(
		redisClient,
		"msp:message_center:search",
		messageCenterSearchRateLimitMax,
		messageCenterRateLimitWindow,
		cfg.RedisFallbackCacheMaxSize,
		logger,
	)
	if err != nil {
		logger.Error("configure message center search rate limit", "error", err)
		os.Exit(1)
	}
	// Message center: conversations
	conversationRepo, err := adapterpostgres.NewConversationRepository(dbPool, wechatReminderEnqueuer)
	if err != nil {
		logger.Error("configure conversation repository", "error", err)
		os.Exit(1)
	}
	conversationService, err := conversationapp.NewService(conversationRepo)
	if err != nil {
		logger.Error("configure conversation service", "error", err)
		os.Exit(1)
	}
	conversationHandler, err := conversationhttp.NewHandler(
		logger,
		conversationService,
		authService,
		conversationhttp.WithRateLimits(messageCenterWriteLimiter, messageCenterSearchLimiter),
	)
	if err != nil {
		logger.Error("configure conversation handler", "error", err)
		os.Exit(1)
	}

	// Message center: notices
	noticeRepo, err := adapterpostgres.NewNoticeRepository(dbPool, wechatReminderEnqueuer)
	if err != nil {
		logger.Error("configure notice repository", "error", err)
		os.Exit(1)
	}
	noticeService, err := noticeapp.NewService(noticeRepo)
	if err != nil {
		logger.Error("configure notice service", "error", err)
		os.Exit(1)
	}
	noticeHandler, err := noticehttp.NewHandler(
		logger,
		noticeService,
		authService,
		noticehttp.WithWriteRateLimit(messageCenterWriteLimiter),
		noticehttp.WithSearchRateLimit(messageCenterSearchLimiter),
	)
	if err != nil {
		logger.Error("configure notice handler", "error", err)
		os.Exit(1)
	}

	// Message center: Q&A threads
	qaThreadRepo, err := adapterpostgres.NewQAThreadRepository(dbPool, wechatReminderEnqueuer)
	if err != nil {
		logger.Error("configure qathread repository", "error", err)
		os.Exit(1)
	}
	qaThreadService, err := qathreadapp.NewService(qaThreadRepo)
	if err != nil {
		logger.Error("configure qathread service", "error", err)
		os.Exit(1)
	}
	qaThreadHandler, err := qathreadhttp.NewHandler(
		logger,
		qaThreadService,
		authService,
		qathreadhttp.WithWriteRateLimit(messageCenterWriteLimiter),
		qathreadhttp.WithSearchRateLimit(messageCenterSearchLimiter),
	)
	if err != nil {
		logger.Error("configure qathread handler", "error", err)
		os.Exit(1)
	}

	// Message center: global forum
	forumRepo, err := adapterpostgres.NewForumRepository(dbPool)
	if err != nil {
		logger.Error("configure forum repository", "error", err)
		os.Exit(1)
	}
	forumService, err := forumapp.NewService(forumRepo)
	if err != nil {
		logger.Error("configure forum service", "error", err)
		os.Exit(1)
	}
	forumHandler, err := forumhttp.NewHandler(
		logger,
		forumService,
		authService,
		forumhttp.WithRateLimits(messageCenterWriteLimiter, messageCenterSearchLimiter),
	)
	if err != nil {
		logger.Error("configure forum handler", "error", err)
		os.Exit(1)
	}

	messageCenterRepo, err := adapterpostgres.NewMessageCenterRepository(dbPool)
	if err != nil {
		logger.Error("configure message center repository", "error", err)
		os.Exit(1)
	}
	messageCenterService, err := messagecenterapp.NewService(messageCenterRepo)
	if err != nil {
		logger.Error("configure message center service", "error", err)
		os.Exit(1)
	}
	messageCenterHandler, err := messagecenterhttp.NewHandler(logger, messageCenterService, authService)
	if err != nil {
		logger.Error("configure message center handler", "error", err)
		os.Exit(1)
	}

	knowledgeRepo, err := adapterpostgres.NewKnowledgeRepository(dbPool)
	if err != nil {
		logger.Error("configure knowledge repository", "error", err)
		os.Exit(1)
	}
	knowledgeService, err := knowledgeapp.NewService(knowledgeRepo)
	if err != nil {
		logger.Error("configure knowledge service", "error", err)
		os.Exit(1)
	}
	knowledgeHandler, err := knowledgehttp.NewHandler(logger, knowledgeService, authService)
	if err != nil {
		logger.Error("configure knowledge handler", "error", err)
		os.Exit(1)
	}
	emailRepo, err := adapterpostgres.NewEmailRepository(dbPool)
	if err != nil {
		logger.Error("configure email repository", "error", err)
		os.Exit(1)
	}
	emailService, err := emailapp.NewService(emailRepo, appCipher, emailadapter.NewSMTPTransport(), logger)
	if err != nil {
		logger.Error("configure email service", "error", err)
		os.Exit(1)
	}
	adminEmailHandler, err := adminemailhttp.NewHandler(logger, emailService, authService)
	if err != nil {
		logger.Error("configure admin email handler", "error", err)
		os.Exit(1)
	}
	adminUserService, err := adminuserapp.NewService(userRepo, emailService)
	if err != nil {
		logger.Error("configure admin user service", "error", err)
		os.Exit(1)
	}
	adminUserHandler, err := adminuserhttp.NewHandler(logger, adminUserService, authService)
	if err != nil {
		logger.Error("configure admin user handler", "error", err)
		os.Exit(1)
	}
	adminInboxService, err := admininboxapp.NewService(userRepo, loginLimiter)
	if err != nil {
		logger.Error("configure admin inbox service", "error", err)
		os.Exit(1)
	}
	adminInboxService.SetEventSender(emailService)
	adminInboxHandler, err := admininboxhttp.NewHandler(logger, adminInboxService, authService)
	if err != nil {
		logger.Error("configure admin inbox handler", "error", err)
		os.Exit(1)
	}
	announcementRepo, err := adapterpostgres.NewAnnouncementRepository(dbPool)
	if err != nil {
		logger.Error("configure announcement repository", "error", err)
		os.Exit(1)
	}
	announcementService, err := announcementapp.NewService(announcementRepo)
	if err != nil {
		logger.Error("configure announcement service", "error", err)
		os.Exit(1)
	}
	announcementHandler, err := announcementhttp.NewHandler(logger, announcementService, authService)
	if err != nil {
		logger.Error("configure announcement handler", "error", err)
		os.Exit(1)
	}
	adminStatsRepo, err := adapterpostgres.NewAdminStatsRepository(dbPool)
	if err != nil {
		logger.Error("configure admin stats repository", "error", err)
		os.Exit(1)
	}
	adminStatsService, err := adminstatsapp.NewService(adminStatsRepo, adminStatusProvider(dbPool, redisClient, qdrantPinger))
	if err != nil {
		logger.Error("configure admin stats service", "error", err)
		os.Exit(1)
	}
	adminStatsService.SetOperationsProvider(adminOperationsProvider(store))
	adminStatsService.SetOperationsResetter(adminstatsapp.OperationsResetterFunc(store.ResetOperationalHTTP))
	adminStatsHandler, err := adminstatshttp.NewHandler(logger, adminStatsService, authService)
	if err != nil {
		logger.Error("configure admin stats handler", "error", err)
		os.Exit(1)
	}
	adminSettingsRepo, err := adapterpostgres.NewAdminSettingsRepository(dbPool)
	if err != nil {
		logger.Error("configure admin settings repository", "error", err)
		os.Exit(1)
	}
	adminSettingsService, err := adminsettingsapp.NewService(adminSettingsRepo, cfg.AppName, cfg.AppVersion, poolStatsProvider(dbPool, cfg))
	if err != nil {
		logger.Error("configure admin settings service", "error", err)
		os.Exit(1)
	}
	adminSettingsHandler, err := adminsettingshttp.NewHandler(logger, adminSettingsService, authService)
	if err != nil {
		logger.Error("configure admin settings handler", "error", err)
		os.Exit(1)
	}
	adminStorageService, err := adminstorageapp.NewService(
		adminSettingsRepo,
		appCipher,
		uploadStorage,
		uploadStorage.LocalConfigured(),
	)
	if err != nil {
		logger.Error("configure admin storage service", "error", err)
		os.Exit(1)
	}
	if err := adminStorageService.ActivateStored(ctx); err != nil {
		logger.Error("activate storage settings", "error", err)
		os.Exit(1)
	}
	adminStorageHandler, err := adminstoragehttp.NewHandler(logger, adminStorageService, authService)
	if err != nil {
		logger.Error("configure admin storage handler", "error", err)
		os.Exit(1)
	}
	securityLogRepo, err := adapterpostgres.NewSecurityLogRepository(dbPool)
	if err != nil {
		logger.Error("configure security log repository", "error", err)
		os.Exit(1)
	}
	securityLogService, err := securitylogapp.NewService(securityLogRepo, securitylogapp.CleanupConfig{
		ArchiveAfterDays: cfg.LogArchiveAfterDays,
		DeleteAfterDays:  cfg.LogDeleteAfterDays,
		BatchSize:        cfg.LogCleanupBatchSize,
		MaxBatches:       cfg.LogCleanupMaxBatches,
		MaxLogCount:      cfg.LogMaxCount,
	})
	if err != nil {
		logger.Error("configure security log service", "error", err)
		os.Exit(1)
	}
	securityLogHandler, err := securityloghttp.NewHandler(logger, securityLogService, authService)
	if err != nil {
		logger.Error("configure security log handler", "error", err)
		os.Exit(1)
	}
	securityLogCleanupWorker, err := securitylogapp.NewCleanupWorker(
		securityLogService,
		logger,
		securitylogapp.CleanupWorkerConfig{
			Interval: cfg.LogCleanupInterval,
			Timeout:  cfg.LogCleanupTimeout,
		},
	)
	if err != nil {
		logger.Error("configure security log cleanup worker", "error", err)
		os.Exit(1)
	}
	uploadService, err := uploadapp.NewService(uploadStorage)
	if err != nil {
		logger.Error("configure upload service", "error", err)
		os.Exit(1)
	}
	uploadAccessRepo, err := adapterpostgres.NewUploadAccessRepository(dbPool)
	if err != nil {
		logger.Error("configure upload access repository", "error", err)
		os.Exit(1)
	}
	uploadHandler, err := uploadhttp.NewHandler(
		logger,
		uploadService,
		authService,
		uploadhttp.WithRedisRateLimit(redisClient, cfg.RedisFallbackCacheMaxSize),
		uploadhttp.WithProtectedLocalDownloads(cfg.UploadsDir, uploadAccessRepo),
	)
	if err != nil {
		logger.Error("configure upload handler", "error", err)
		os.Exit(1)
	}
	xidianRepo, err := adapterpostgres.NewXidianRepository(dbPool)
	if err != nil {
		logger.Error("configure xidian repository", "error", err)
		os.Exit(1)
	}
	xidianPortalClient, err := xidianintegration.NewClient(xidianintegration.Config{
		IDsBase:        cfg.XidianIDsBase,
		EhallBase:      cfg.XidianEhallBase,
		UserAgent:      cfg.XidianUserAgent,
		ConnectTimeout: cfg.XidianHTTPConnectTimeout,
		ReadTimeout:    cfg.XidianHTTPReadTimeout,
		RetryCount:     cfg.XidianHTTPRetryCount,
		CaptchaWidth:   cfg.XidianCaptchaWidth,
	})
	if err != nil {
		logger.Error("configure xidian portal client", "error", err)
		os.Exit(1)
	}
	xidianService, err := xidianapp.NewService(xidianRepo, xidianPortalClient, xidianapp.NewMemoryChallengeStore(cfg.RedisFallbackCacheMaxSize), xidianapp.Config{
		ChallengeTTL:  cfg.XidianChallengeTTL,
		CaptchaWidth:  cfg.XidianCaptchaWidth,
		CaptchaHeight: cfg.XidianCaptchaHeight,
		PieceWidth:    cfg.XidianPieceWidth,
		PieceHeight:   cfg.XidianPieceHeight,
	})
	if err != nil {
		logger.Error("configure xidian service", "error", err)
		os.Exit(1)
	}
	xidianHandler, err := xidianhttp.NewHandler(logger, xidianService, authService)
	if err != nil {
		logger.Error("configure xidian handler", "error", err)
		os.Exit(1)
	}
	wechatRepo, err := adapterpostgres.NewWechatRepository(dbPool)
	if err != nil {
		logger.Error("configure wechat repository", "error", err)
		os.Exit(1)
	}
	wechatState, err := adapterredis.NewWechatStateStore(redisClient)
	if err != nil {
		logger.Error("configure wechat state store", "error", err)
		os.Exit(1)
	}
	var wechatProtocol *wechatintegration.Protocol
	var wechatTextSender wechatapp.Sender
	var wechatTemplateSender wechatreminder.Sender
	if cfg.WechatOfficialAccountEnabled {
		wechatProtocol, err = wechatintegration.NewProtocol(
			cfg.WechatOfficialAccountAppID,
			cfg.WechatOfficialAccountToken,
			cfg.WechatOfficialAccountAESKey,
		)
		if err != nil {
			logger.Error("configure wechat callback protocol", "error", err)
			os.Exit(1)
		}
		wechatAPIClient, clientErr := wechatintegration.NewAPIClient(
			cfg.WechatOfficialAccountAppID,
			cfg.WechatOfficialAccountAppSecret,
			outbound.NewPublicHTTPSClient(cfg.WechatOfficialAccountHTTPTimeout),
			wechatState,
		)
		if clientErr != nil {
			logger.Error("configure wechat API client", "error", clientErr)
			os.Exit(1)
		}
		wechatTextSender = wechatAPIClient
		wechatTemplateSender = wechatAPIClient
	}
	wechatService, err := wechatapp.NewService(wechatRepo, wechatState, wechatTextSender, wechatapp.Config{
		Enabled:            cfg.WechatOfficialAccountEnabled,
		AppID:              cfg.WechatOfficialAccountAppID,
		AccountName:        cfg.WechatOfficialAccountName,
		BindingTicketTTL:   10 * time.Minute,
		EventDedupeTTL:     24 * time.Hour,
		EventProcessingTTL: 6 * time.Second,
	})
	if err != nil {
		logger.Error("configure wechat service", "error", err)
		os.Exit(1)
	}
	wechatHandler, err := wechathttp.NewHandler(logger, wechatService, authService, wechatProtocol, wechathttp.Config{
		Enabled:     cfg.WechatOfficialAccountEnabled,
		MessageMode: cfg.WechatOfficialAccountMessageMode,
	}, wechathttp.WithRedisRateLimit(redisClient, cfg.RedisFallbackCacheMaxSize))
	if err != nil {
		logger.Error("configure wechat handler", "error", err)
		os.Exit(1)
	}
	wechatReminderRepo, err := adapterpostgres.NewWechatReminderRepository(dbPool)
	if err != nil {
		logger.Error("configure wechat reminder repository", "error", err)
		os.Exit(1)
	}
	wechatReminderWorker, err := wechatreminder.NewWorker(
		wechatReminderRepo,
		wechatTemplateSender,
		wechatreminder.ClassifySendError,
		logger,
		wechatreminder.Config{
			Enabled:                  cfg.WechatMessageRemindersEnabled,
			AppID:                    cfg.WechatOfficialAccountAppID,
			PrivateMessageTemplateID: cfg.WechatPrivateMessageTemplateID,
			NoticeTemplateID:         cfg.WechatNoticeTemplateID,
			QAMessageTemplateID:      cfg.WechatQAMessageTemplateID,
			SendTimeout:              cfg.WechatOfficialAccountHTTPTimeout,
		},
	)
	if err != nil {
		logger.Error("configure wechat reminder worker", "error", err)
		os.Exit(1)
	}

	checker := health.NewChecker(cfg.AppVersion, dbPool, health.RedisPingerFunc(func(ctx context.Context) error {
		return redisClient.Ping(ctx).Err()
	}), qdrantPinger)

	handler, err := httpserver.NewHandler(
		cfg,
		logger,
		checker,
		store,
		httpserver.WithRoutes(func(mux *http.ServeMux) {
			openAIHandler.Register(mux)
			authHandler.Register(mux, cfg.APIV1Prefix+"/auth")
			wechatHandler.RegisterPublic(mux, cfg.APIV1Prefix+"/integrations/wechat")
			wechatHandler.RegisterUser(mux, cfg.APIV1Prefix+"/integrations/wechat")
			announcementHandler.RegisterUser(mux, cfg.APIV1Prefix+"/announcements")
			progressHandler.Register(mux, cfg.APIV1Prefix+"/progress")
			portraitHandler.Register(mux, cfg.APIV1Prefix+"/portrait")
			mistakeHandler.Register(mux, cfg.APIV1Prefix+"/mistakes")
			exerciseHandler.Register(mux, cfg.APIV1Prefix+"/exercise")
			dailyQuestionHandler.Register(mux, cfg.APIV1Prefix+"/daily-question")
			sessionHandler.Register(mux, cfg.APIV1Prefix+"/session")
			resourceHandler.Register(mux, cfg.APIV1Prefix+"/resources")
			uploadHandler.Register(mux, cfg.APIV1Prefix+"/upload")
			xidianHandler.Register(mux, cfg.APIV1Prefix+"/xidian")
			questionHandler.Register(mux, cfg.APIV1Prefix+"/questions")
			classHandler.Register(mux, cfg.APIV1Prefix+"/classes")
			teacherHandler.Register(mux, cfg.APIV1Prefix+"/teacher")
			conversationHandler.Register(mux, cfg.APIV1Prefix+"/conversations")
			noticeHandler.Register(mux, cfg.APIV1Prefix+"/notices")
			qaThreadHandler.Register(mux, cfg.APIV1Prefix+"/qa-threads")
			forumHandler.Register(mux, cfg.APIV1Prefix+"/forum")
			messageCenterHandler.Register(mux, cfg.APIV1Prefix+"/message-center")
			adminUserHandler.Register(mux, cfg.APIV1Prefix+"/admin/users")
			aiRiskHandler.Register(mux, cfg.APIV1Prefix+"/admin/risk-control")
			adminInboxHandler.Register(mux, cfg.APIV1Prefix+"/admin/inbox")
			announcementHandler.RegisterAdmin(mux, cfg.APIV1Prefix+"/admin/announcements")
			adminAIConfigHandler.Register(mux, cfg.APIV1Prefix+"/admin/ai-config")
			adminStatsHandler.Register(mux, cfg.APIV1Prefix+"/admin/stats")
			adminSettingsHandler.Register(mux, cfg.APIV1Prefix+"/admin/settings")
			adminStorageHandler.Register(mux, cfg.APIV1Prefix+"/admin/settings")
			adminEmailHandler.Register(mux, cfg.APIV1Prefix+"/admin/settings")
			securityLogHandler.Register(mux, cfg.APIV1Prefix+"/admin/security-logs")
			knowledgeHandler.Register(mux, cfg.APIV1Prefix+"/admin/knowledge")
			wechatHandler.RegisterAdmin(mux, cfg.APIV1Prefix+"/admin/wechat")
		}),
	)
	if err != nil {
		logger.Error("configure http handler", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddr(),
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stopCh)

	stopSecurityLogCleanupWorker := startSecurityLogCleanupWorker(
		securityLogCleanupWorker,
		cfg.LogCleanupEnabled,
		cfg.ShutdownTimeout,
		logger,
	)
	stopWechatReminderWorker := startWechatReminderWorker(
		wechatReminderWorker,
		cfg.WechatMessageRemindersEnabled,
		cfg.ShutdownTimeout,
		logger,
	)
	stopDailyQuestionWorker := startDailyQuestionWorker(
		dailyQuestionWorker,
		cfg.DailyQuestionPreGenerationEnabled,
		cfg.ShutdownTimeout,
		logger,
	)
	stopDailyQuestionReminderWorker := startDailyQuestionReminderWorker(
		dailyQuestionReminderWorker,
		cfg.WechatMessageRemindersEnabled,
		cfg.ShutdownTimeout,
		logger,
	)
	logger.Info("Go API listening", "addr", cfg.HTTPAddr(), "environment", cfg.Environment)
	serveErr := serveHTTP(server, stopCh, cfg.ShutdownTimeout, logger)
	stopSecurityLogCleanupWorker()
	stopDailyQuestionReminderWorker()
	stopDailyQuestionWorker()
	stopWechatReminderWorker()
	if serveErr != nil {
		os.Exit(1)
	}
}

func newLogger(cfg config.Config) *slog.Logger {
	level := slog.LevelInfo
	if cfg.Debug {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

func defaultEinoConfig(cfg config.Config) einoagent.Config {
	return einoagent.Config{
		Enabled:       cfg.EinoEnabled,
		BaseURL:       cfg.EinoBaseURL,
		APIKey:        cfg.EinoAPIKey,
		Model:         cfg.EinoModel,
		Timeout:       cfg.EinoTimeout,
		Temperature:   cfg.EinoTemperature,
		MaxTokens:     cfg.EinoMaxTokens,
		MaxIterations: cfg.EinoMaxIterations,
	}
}

func requireSharedRedis(ctx context.Context, cfg config.Config, redisClient *goredis.Client) error {
	if !cfg.RequiresSharedRefreshSessionStore() && !cfg.WechatOfficialAccountEnabled {
		return nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, cfg.RedisConnectTimeout+cfg.RedisSocketTimeout)
	defer cancel()
	return redisClient.Ping(checkCtx).Err()
}

func adminStatusProvider(dbPool *pgxpool.Pool, redisClient *goredis.Client, optional ...health.Pinger) adminstatsapp.StatusProviderFunc {
	return func(ctx context.Context) ([]adminstatsapp.ServiceStatus, error) {
		statuses := []adminstatsapp.ServiceStatus{
			pingStatus(ctx, "PostgreSQL", func(ctx context.Context) error { return dbPool.Ping(ctx) }),
			pingStatus(ctx, "Redis", func(ctx context.Context) error { return redisClient.Ping(ctx).Err() }),
		}
		if len(optional) > 0 && optional[0] != nil {
			statuses = append(statuses, pingStatus(ctx, "Qdrant", optional[0].Ping))
		}
		return statuses, nil
	}
}

func adminOperationsProvider(store *metrics.Store) adminstatsapp.OperationsProviderFunc {
	return func() adminstatsapp.OperationsSnapshot {
		snapshot := store.OperationalSnapshot()
		return adminstatsapp.OperationsSnapshot{
			Version:           snapshot.Version,
			Environment:       snapshot.Environment,
			StartedAt:         snapshot.StartedAt,
			Uptime:            snapshot.Uptime,
			CPUUsagePercent:   snapshot.Process.CPUUsagePercent,
			HeapUsedBytes:     snapshot.Process.HeapUsedBytes,
			HeapReservedBytes: snapshot.Process.HeapReservedBytes,
			Goroutines:        snapshot.Process.Goroutines,
			LogicalCPUs:       snapshot.Process.LogicalCPUs,
			GOMAXPROCS:        snapshot.Process.GOMAXPROCS,
			GoVersion:         snapshot.Process.GoVersion,
			OS:                snapshot.Process.OS,
			Arch:              snapshot.Process.Arch,
			RequestsTotal:     snapshot.HTTP.RequestsTotal,
			ClientErrorsTotal: snapshot.HTTP.ClientErrorsTotal,
			ServerErrorsTotal: snapshot.HTTP.ServerErrorsTotal,
			AverageLatency:    snapshot.HTTP.AverageDuration,
			P95Latency:        snapshot.HTTP.P95Duration,
			P95Clamped:        snapshot.HTTP.P95Clamped,
			TrafficStartedAt:  snapshot.TrafficStartedAt,
			TrafficWindow:     snapshot.TrafficWindow,
			PostgreSQL: adminstatsapp.DatabasePoolSnapshot{
				MaxConnections:       snapshot.Dependencies.Postgres.MaxConnections,
				TotalConnections:     snapshot.Dependencies.Postgres.TotalConnections,
				AcquiredConnections:  snapshot.Dependencies.Postgres.AcquiredConnections,
				IdleConnections:      snapshot.Dependencies.Postgres.IdleConnections,
				EmptyAcquireCount:    snapshot.Dependencies.Postgres.EmptyAcquireCount,
				CanceledAcquireCount: snapshot.Dependencies.Postgres.CanceledAcquireCount,
			},
			Redis: adminstatsapp.RedisPoolSnapshot{
				MaxConnections:   snapshot.Dependencies.Redis.MaxConnections,
				TotalConnections: snapshot.Dependencies.Redis.TotalConnections,
				IdleConnections:  snapshot.Dependencies.Redis.IdleConnections,
				StaleConnections: snapshot.Dependencies.Redis.StaleConnections,
				Hits:             snapshot.Dependencies.Redis.Hits,
				Misses:           snapshot.Dependencies.Redis.Misses,
				Timeouts:         snapshot.Dependencies.Redis.Timeouts,
				WaitCount:        snapshot.Dependencies.Redis.WaitCount,
				Unusable:         snapshot.Dependencies.Redis.Unusable,
			},
		}
	}
}

func pingStatus(ctx context.Context, name string, ping func(context.Context) error) adminstatsapp.ServiceStatus {
	start := time.Now()
	status := "running"
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := ping(checkCtx); err != nil {
		status = "stopped"
	}
	latency := float64(time.Since(start).Microseconds()) / 1000
	return adminstatsapp.ServiceStatus{Name: name, Status: status, LatencyMS: &latency}
}

func poolStatsProvider(dbPool *pgxpool.Pool, cfg config.Config) adminsettingsapp.PoolStatsProviderFunc {
	return func() adminsettingsapp.ConnectionPoolStatus {
		stats := dbPool.Stat()
		maxConns := int(stats.MaxConns())
		acquired := int(stats.AcquiredConns())
		idle := int(stats.IdleConns())
		usage := 0.0
		if maxConns > 0 {
			usage = float64(acquired) / float64(maxConns) * 100
		}
		return adminsettingsapp.ConnectionPoolStatus{
			PoolSize:     maxConns,
			MaxOverflow:  0,
			CheckedOut:   acquired,
			CheckedIn:    idle,
			Overflow:     0,
			PoolTimeout:  int(cfg.DBConnectTimeout.Seconds()),
			PoolRecycle:  int(cfg.DBPoolRecycle.Seconds()),
			UsagePercent: usage,
		}
	}
}
