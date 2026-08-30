// Package dailyquestion implements one fixed, Shanghai-calendar question per student day.
package dailyquestion

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	airiskapp "mathstudy/backend/internal/application/airisk"
	exerciseapp "mathstudy/backend/internal/application/exercise"
	"mathstudy/backend/internal/platform/identifier"
	"mathstudy/backend/internal/platform/questiondedupe"
)

const (
	StatusNotStarted  = "not_started"
	StatusPreparing   = "preparing"
	StatusReady       = "ready"
	StatusCompleted   = "completed"
	StatusUnavailable = "unavailable"

	SourceTeacherCandidate = "teacher_candidate"
	SourceTeacherBank      = "teacher_bank"
	SourceAIGenerated      = "ai_generated"

	ReasonMistakeReview   = "mistake_review"
	ReasonLearningGoal    = "learning_goal"
	ReasonWeakest         = "weakest_concept"
	ReasonDefault         = "default_concept"
	ReasonTeacherFallback = "teacher_concept_fallback"
	ReasonTeacherUniform  = "teacher_uniform"
	ReasonRepeatFallback  = "repeat_fallback"

	FailureTeacherNotAssigned = "teacher_not_assigned"

	StrategyPersonalized = "personalized"
	StrategyUniform      = "uniform"

	FirstResultCorrect   = "correct"
	FirstResultIncorrect = "incorrect"

	ReminderKindManualStudent    = "manual_student_reminder"
	ReminderKindAutomaticStudent = "automatic_student_reminder"

	defaultHistoryDays              = 7
	maxHistoryDays                  = 366
	MaxUniformScheduleItems         = 60
	defaultStalePreparation         = 2 * time.Minute
	recentAttemptExclusion          = 20
	historicalQuestionBodyLimit     = 200
	maxBackgroundPreparationRetries = 3
)

var (
	ErrNotFound                   = errors.New("daily question not found")
	ErrInvalidDate                = errors.New("invalid daily question date")
	ErrInvalidMonth               = errors.New("invalid daily question month")
	ErrInvalidDays                = errors.New("invalid daily question history days")
	ErrInvalidStrategy            = errors.New("invalid daily question class strategy")
	ErrForbidden                  = errors.New("daily question forbidden")
	ErrRateLimited                = errors.New("daily question generation rate limited")
	ErrInvalidContent             = errors.New("invalid daily question content")
	ErrDuplicateQuestion          = errors.New("daily question duplicate")
	ErrSelectionLocked            = errors.New("daily question class selection is locked")
	ErrUniformScheduleChanged     = errors.New("daily question uniform schedule changed")
	ErrUniformQuestionNotAssigned = errors.New("daily question uniform class has no question assigned")
	ErrStrategyChanged            = errors.New("daily question class strategy changed during assignment")
	ErrReminderUnavailable        = errors.New("daily question wechat reminder is unavailable")
)

var shanghaiLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

// Repository is the persistence surface for daily-question assignment flows.
// The exercise submission path deliberately owns its own transactional update
// of first_attempt_id and completed_at so attempt persistence remains atomic.
type Repository interface {
	GetAssignment(context.Context, string, time.Time) (Assignment, bool, error)
	ListAssignments(context.Context, string, time.Time, time.Time) ([]Assignment, error)
	ListStreakDates(context.Context, string, time.Time, time.Time) ([]time.Time, error)
	GetStudentContext(context.Context, string, time.Time) (StudentContext, error)
	SelectTargetConcept(context.Context, string) (TargetSelection, bool, error)
	ListHistoricalQuestionBodies(context.Context, string, time.Time, int) ([]string, error)
	ListRecentAttemptedContentIDs(context.Context, string, int) ([]string, error)
	FindTeacherContent(context.Context, string, string, time.Time, bool, string, []string, []string) (ContentChoice, bool, error)
	FindTeacherRepeatFallback(context.Context, string, string, time.Time, bool, string, []string) (ContentChoice, bool, error)
	GetClassSelection(context.Context, string, time.Time) (ClassSelection, bool, error)
	GetClassUniformSchedule(context.Context, string, string, time.Time, int) (ClassUniformSchedule, bool, error)
	ReplaceClassUniformSchedule(context.Context, UniformScheduleInput) (ClassUniformSchedule, bool, error)
	ReservePreparation(context.Context, PreparationReservation) (Assignment, bool, error)
	FinishPreparation(context.Context, string, string, ContentChoice, string, string, time.Time) (bool, error)
	FailPreparation(context.Context, string, string, string, time.Time) error
	MarkOpened(context.Context, string, time.Time, time.Time) (Assignment, bool, error)
	GetClassSettings(context.Context, string, string, time.Time) (ClassSettings, bool, error)
	UpsertClassSettings(context.Context, ClassSettingsUpdate, time.Time, time.Time) (ClassSettingsUpdateResult, bool, error)
	GetClassStatistics(context.Context, string, string, time.Time) (ClassStatistics, bool, error)
	CreateClassReminder(context.Context, ClassReminderInput) (ReminderResult, bool, error)
	DispatchAutomaticReminders(context.Context, time.Time, time.Time) error
	DispatchUniformLowStockAlerts(context.Context, time.Time, time.Time) error
	EnsureUniformLowStockAlert(context.Context, string, string, time.Time, time.Time) error
}

// ExerciseGenerator is the existing validated AI exercise generation surface.
// GenerateExercise already rejects content that its independent Solver cannot verify.
type ExerciseGenerator interface {
	GenerateExercise(context.Context, string, exerciseapp.GenerateExerciseRequest) (*exerciseapp.ExerciseResponse, error)
}

// AIRequestGuard applies student AI policy only when teacher content cannot satisfy the task.
type AIRequestGuard interface {
	Acquire(context.Context, string, string, string, bool) (airiskapp.Lease, error)
}

// GenerationLimiter bounds repeated AI fallbacks while allowing idempotent reads and teacher content.
type GenerationLimiter interface {
	Allow(context.Context, string) bool
}

// Assignment is the persisted daily task read model.
type Assignment struct {
	ID                 string
	StudentID          string
	ClassID            *string
	AssignmentDate     time.Time
	ContentID          *string
	TargetConceptID    *string
	TargetConceptName  *string
	Source             string
	SelectionReason    string
	Status             string
	FirstAttemptID     *string
	CorrectedAttemptID *string
	FirstResult        *string
	CountsTowardStreak bool
	RetryCount         int
	FailureCode        *string
	AssignedAt         time.Time
	OpenedAt           *time.Time
	CompletedAt        *time.Time
	UpdatedAt          time.Time
	GenerationToken    *string
	Question           *exerciseapp.ExerciseResponse
}

// StudentContext contains the current class snapshot used for source selection.
type StudentContext struct {
	ClassID             string
	TeacherID           string
	ClassStrategy       string
	PreferredDifficulty float64
}

// TargetSelection is the selected knowledge point and its deterministic priority reason.
type TargetSelection struct {
	ConceptID string
	Reason    string
}

// ContentChoice is a published teacher-owned question eligible for assignment.
type ContentChoice struct {
	ContentID       string
	TargetConceptID string
}

// ClassSelection stores the persisted same-question choice for a uniform class day.
type ClassSelection struct {
	ClassID         string
	AssignmentDate  time.Time
	ContentID       string
	TargetConceptID string
	Source          string
	SelectionReason string
	QuestionBody    string
}

// PreparationReservation atomically creates or claims a recoverable preparing row.
type PreparationReservation struct {
	AssignmentID    string
	GenerationToken string
	StudentID       string
	ClassID         string
	AssignmentDate  time.Time
	TargetConceptID string
	SelectionReason string
	ClassStrategy   string
	Now             time.Time
	OpenedAt        *time.Time
	StaleBefore     time.Time
}

// ClassSettings configures a teacher class's daily-question selection strategy.
type ClassSettings struct {
	ClassID                     string `json:"class_id"`
	TeacherID                   string `json:"teacher_id,omitempty"`
	Strategy                    string `json:"strategy"`
	EffectiveStrategy           string `json:"effective_strategy"`
	EffectiveDate               string `json:"effective_date"`
	TodayAssignmentCount        int    `json:"today_assignment_count"`
	UniformReady                bool   `json:"uniform_ready"`
	AutoReminderEnabled         bool   `json:"auto_reminder_enabled"`
	TodayReminderSent           bool   `json:"today_reminder_sent"`
	TodayReminderRecipientCount int    `json:"today_reminder_recipient_count"`
}

// ClassSettingsUpdate carries only the teacher settings explicitly changed by one request.
type ClassSettingsUpdate struct {
	ClassID             string
	TeacherID           string
	Strategy            *string
	AutoReminderEnabled *bool
}

// ClassSettingsUpdateResult includes the committed settings and transition metadata.
type ClassSettingsUpdateResult struct {
	Settings                ClassSettings
	AutoReminderJustEnabled bool
}

// ClassUniformScheduleItem is one teacher-managed class-day question.
type ClassUniformScheduleItem struct {
	AssignmentDate  string  `json:"assignment_date"`
	ContentID       string  `json:"content_id"`
	TargetConceptID *string `json:"target_concept_id"`
	Title           string  `json:"title"`
	Body            string  `json:"body"`
	Difficulty      float64 `json:"difficulty"`
	Locked          bool    `json:"locked"`
}

// ClassUniformSchedule is the ordered Shanghai-calendar plan for one class.
type ClassUniformSchedule struct {
	ClassID         string                     `json:"class_id"`
	StartDate       string                     `json:"start_date"`
	ScheduleVersion int64                      `json:"schedule_version"`
	Items           []ClassUniformScheduleItem `json:"items"`
}

// UniformScheduleInput replaces the editable part of a class schedule.
type UniformScheduleInput struct {
	TeacherID       string
	ClassID         string
	Today           time.Time
	ScheduleVersion int64
	ContentIDs      []string
	Now             time.Time
}

// WeakConcept is one class aggregate used by the teacher view.
type WeakConcept struct {
	ConceptID   string `json:"concept_id"`
	ConceptName string `json:"concept_name"`
	WrongCount  int    `json:"wrong_count"`
}

// ClassStatistics summarizes daily completion without ranking personalized tasks.
type ClassStatistics struct {
	ClassID           string        `json:"class_id"`
	AssignmentDate    string        `json:"assignment_date"`
	StudentCount      int           `json:"student_count"`
	AssignedCount     int           `json:"assigned_count"`
	CompletedCount    int           `json:"completed_count"`
	FirstCorrectCount int           `json:"first_correct_count"`
	CorrectedCount    int           `json:"corrected_count"`
	CompletionRate    float64       `json:"completion_rate"`
	FirstCorrectRate  float64       `json:"first_correct_rate"`
	CorrectionRate    float64       `json:"correction_rate"`
	WeakConcepts      []WeakConcept `json:"weak_concepts"`
}

// ClassReminderInput creates one WeChat-only student reminder event.
type ClassReminderInput struct {
	ReminderID     string
	TeacherID      string
	ClassID        string
	AssignmentDate time.Time
	Kind           string
	Now            time.Time
}

// ReminderResult describes one reminder request. RecipientCount is the number
// of jobs newly queued or requeued by this request, not the day's cumulative total.
type ReminderResult struct {
	ReminderID     string `json:"reminder_id"`
	RecipientCount int    `json:"recipient_count"`
	Created        bool   `json:"created"`
}

// TodayResponse is intentionally flat so the home card and dedicated page can share it.
type TodayResponse struct {
	Status             string                        `json:"status"`
	AssignmentID       *string                       `json:"assignment_id"`
	AssignmentDate     string                        `json:"assignment_date"`
	ContentID          *string                       `json:"content_id"`
	TargetConceptID    *string                       `json:"target_concept_id"`
	TargetConceptName  *string                       `json:"target_concept_name"`
	Source             *string                       `json:"source"`
	SelectionReason    *string                       `json:"selection_reason"`
	FirstAttemptID     *string                       `json:"first_attempt_id"`
	CorrectedAttemptID *string                       `json:"corrected_attempt_id"`
	FirstResult        *string                       `json:"first_result"`
	CountsTowardStreak bool                          `json:"counts_toward_streak"`
	StreakDays         int                           `json:"streak_days"`
	AssignedAt         *string                       `json:"assigned_at"`
	OpenedAt           *string                       `json:"opened_at"`
	CompletedAt        *string                       `json:"completed_at"`
	UpdatedAt          *string                       `json:"updated_at"`
	FailureCode        *string                       `json:"failure_code"`
	Question           *exerciseapp.ExerciseResponse `json:"question"`
	RetryCount         int                           `json:"-"`
}

// HistoryQuery accepts either a Shanghai month (YYYY-MM) or the latest N days.
type HistoryQuery struct {
	Month string
	Days  int
}

// HistoryResponse is used by the recent-seven-days strip and the later calendar view.
type HistoryResponse struct {
	Items      []TodayResponse `json:"items"`
	StreakDays int             `json:"streak_days"`
}

// Service implements student and teacher daily-question use cases.
type Service struct {
	repo                  Repository
	generator             ExerciseGenerator
	guard                 AIRequestGuard
	limiter               GenerationLimiter
	now                   func() time.Time
	newID                 func() (string, error)
	preparationStaleAfter time.Duration
	remindersEnabled      bool
}

// Option customizes the daily-question service.
type Option func(*Service)

// WithClock is useful to deterministic callers that need a fixed Shanghai date.
func WithClock(now func() time.Time) Option {
	return func(service *Service) {
		if now != nil {
			service.now = now
		}
	}
}

// WithPreparationStaleAfter changes the recovery window for abandoned AI generation.
func WithPreparationStaleAfter(value time.Duration) Option {
	return func(service *Service) {
		if value > 0 {
			service.preparationStaleAfter = value
		}
	}
}

// WithAIRequestGuard protects the AI fallback without blocking teacher-bank assignments.
func WithAIRequestGuard(guard AIRequestGuard) Option {
	return func(service *Service) {
		service.guard = guard
	}
}

// WithGenerationLimiter applies the shared exercise-generation frequency policy to AI fallback.
func WithGenerationLimiter(limiter GenerationLimiter) Option {
	return func(service *Service) {
		service.limiter = limiter
	}
}

// WithWechatRemindersEnabled makes WeChat-only daily reminders available when
// the application has a configured durable WeChat reminder worker.
func WithWechatRemindersEnabled(enabled bool) Option {
	return func(service *Service) {
		service.remindersEnabled = enabled
	}
}

// NewService creates a daily-question service. A nil generator is allowed so
// published teacher questions remain usable while AI fallback becomes unavailable.
func NewService(repo Repository, generator ExerciseGenerator, options ...Option) (*Service, error) {
	if repo == nil {
		return nil, errors.New("daily question repository is nil")
	}
	service := &Service{
		repo:                  repo,
		generator:             generator,
		now:                   time.Now,
		newID:                 identifier.NewUUID,
		preparationStaleAfter: defaultStalePreparation,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

// GetToday only reads state. It intentionally never starts an AI request.
func (s *Service) GetToday(ctx context.Context, studentID string) (TodayResponse, error) {
	today := shanghaiDay(s.now())
	streak, err := s.streakDays(ctx, studentID, today)
	if err != nil {
		return TodayResponse{}, err
	}
	assignment, found, err := s.repo.GetAssignment(ctx, studentID, today)
	if err != nil {
		return TodayResponse{}, err
	}
	if !found {
		student, err := s.repo.GetStudentContext(ctx, studentID, today)
		if err != nil {
			return TodayResponse{}, err
		}
		if student.ClassStrategy == StrategyUniform && student.ClassID != "" {
			_, assigned, err := s.repo.GetClassSelection(ctx, student.ClassID, today)
			if err != nil {
				return TodayResponse{}, err
			}
			if !assigned {
				return teacherNotAssignedResponse(today, streak), nil
			}
		}
		return notStartedResponse(today, streak), nil
	}
	return assignmentResponse(assignment, streak), nil
}

// PrepareToday creates a fixed task only after the student enters the dedicated page.
// AI work is synchronous to reuse the existing verified generation pipeline; a durable
// preparing row lets concurrent requests observe progress and recover abandoned work.
func (s *Service) PrepareToday(ctx context.Context, studentID string) (TodayResponse, error) {
	rawNow := s.now()
	return s.prepareDate(ctx, studentID, shanghaiDay(rawNow), rawNow, 0, true, false)
}

// PrepareTodayInBackground assigns today's question without recording a student page visit.
func (s *Service) PrepareTodayInBackground(ctx context.Context, studentID string) (TodayResponse, error) {
	rawNow := s.now()
	assignmentDate := shanghaiDay(rawNow)
	response, err := s.prepareDate(ctx, studentID, assignmentDate, rawNow, 0, false, false)
	if err == nil || response.AssignmentID != nil {
		return response, err
	}

	// Preparation errors are persisted before they are returned. Reload with a
	// short independent context so the worker can classify and bound retries
	// even when the per-student generation context has already expired.
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	assignment, found, readErr := s.repo.GetAssignment(readCtx, studentID, assignmentDate)
	if readErr == nil && found {
		return assignmentResponse(assignment, 0), err
	}
	return response, err
}

// prepareDate creates today's task or resumes an existing historical task after
// its generation lease expires. Historical recovery must not create a missing
// task: only the already persisted assignment is eligible for that path.
func (s *Service) prepareDate(
	ctx context.Context,
	studentID string,
	assignmentDate time.Time,
	rawNow time.Time,
	strategyRetry int,
	markOpened bool,
	requireExisting bool,
) (TodayResponse, error) {
	today := shanghaiDay(rawNow)
	now := shanghaiNow(rawNow)
	assignmentDate = shanghaiDay(assignmentDate)
	if assignmentDate.After(today) {
		return TodayResponse{}, ErrNotFound
	}
	streak := 0
	if markOpened {
		var err error
		streak, err = s.streakDays(ctx, studentID, today)
		if err != nil {
			return TodayResponse{}, err
		}
	}

	current, found, err := s.repo.GetAssignment(ctx, studentID, assignmentDate)
	if err != nil {
		return TodayResponse{}, err
	}
	if !found && requireExisting {
		return TodayResponse{}, ErrNotFound
	}
	if found {
		if !markOpened && !requireExisting &&
			current.RetryCount >= maxBackgroundPreparationRetries &&
			current.Status != StatusPreparing {
			return assignmentResponse(current, streak), nil
		}
		switch current.Status {
		case StatusCompleted:
			return assignmentResponse(current, streak), nil
		case StatusPreparing:
			if markOpened && current.OpenedAt == nil {
				opened, ok, err := s.repo.MarkOpened(ctx, studentID, assignmentDate, now)
				if err != nil {
					return TodayResponse{}, err
				}
				if ok {
					current = opened
				}
			}
			switch current.Status {
			case StatusCompleted:
				return assignmentResponse(current, streak), nil
			case StatusReady:
				if current.Question != nil {
					return assignmentResponse(current, streak), nil
				}
			case StatusPreparing:
				if !s.isPreparationStale(current, now) {
					return assignmentResponse(current, streak), nil
				}
				if !markOpened && !requireExisting &&
					current.RetryCount >= maxBackgroundPreparationRetries &&
					current.GenerationToken != nil {
					return s.failPreparation(
						ctx,
						current,
						*current.GenerationToken,
						"background_retry_exhausted",
						now,
						streak,
					)
				}
			}
		case StatusReady:
			if current.Question != nil {
				if !markOpened {
					return assignmentResponse(current, streak), nil
				}
				opened, ok, err := s.repo.MarkOpened(ctx, studentID, assignmentDate, now)
				if err != nil {
					return TodayResponse{}, err
				}
				if ok {
					return assignmentResponse(opened, streak), nil
				}
				return assignmentResponse(current, streak), nil
			}
		}
	}

	studentContext, err := s.repo.GetStudentContext(ctx, studentID, assignmentDate)
	if err != nil {
		return TodayResponse{}, err
	}
	var target TargetSelection
	if studentContext.ClassStrategy == StrategyUniform && studentContext.ClassID != "" {
		selection, assigned, err := s.repo.GetClassSelection(ctx, studentContext.ClassID, assignmentDate)
		if err != nil {
			return TodayResponse{}, err
		}
		if !assigned {
			if current.Status == StatusPreparing && s.isPreparationStale(current, now) && current.GenerationToken != nil {
				return s.failPreparation(ctx, current, *current.GenerationToken, FailureTeacherNotAssigned, now, streak)
			}
			return teacherNotAssignedResponse(assignmentDate, streak), nil
		}
		target = TargetSelection{ConceptID: selection.TargetConceptID, Reason: ReasonTeacherUniform}
	} else {
		var targetFound bool
		target, targetFound, err = s.repo.SelectTargetConcept(ctx, studentID)
		if err != nil {
			return TodayResponse{}, err
		}
		if !targetFound {
			target = TargetSelection{Reason: ReasonDefault}
		}
	}
	assignmentID, err := s.newID()
	if err != nil {
		return TodayResponse{}, fmt.Errorf("new daily assignment id: %w", err)
	}
	generationToken, err := s.newID()
	if err != nil {
		return TodayResponse{}, fmt.Errorf("new daily generation token: %w", err)
	}
	var openedAt *time.Time
	if markOpened {
		openedAt = &now
	}
	reservation, claimed, err := s.repo.ReservePreparation(ctx, PreparationReservation{
		AssignmentID:    assignmentID,
		GenerationToken: generationToken,
		StudentID:       studentID,
		ClassID:         studentContext.ClassID,
		AssignmentDate:  assignmentDate,
		TargetConceptID: target.ConceptID,
		SelectionReason: target.Reason,
		ClassStrategy:   studentContext.ClassStrategy,
		Now:             now,
		OpenedAt:        openedAt,
		StaleBefore:     now.Add(-s.preparationStaleAfter),
	})
	if errors.Is(err, ErrStrategyChanged) && strategyRetry < 2 {
		return s.prepareDate(ctx, studentID, assignmentDate, rawNow, strategyRetry+1, markOpened, requireExisting)
	}
	if err != nil {
		return TodayResponse{}, err
	}
	if !claimed {
		return assignmentResponse(reservation, streak), nil
	}

	if target.ConceptID == "" && studentContext.ClassStrategy != StrategyUniform {
		return s.failPreparation(ctx, reservation, generationToken, "target_concept_unavailable", now, streak)
	}

	historicalBodies, err := s.repo.ListHistoricalQuestionBodies(
		ctx,
		studentID,
		assignmentDate,
		historicalQuestionBodyLimit,
	)
	if err != nil {
		return TodayResponse{}, s.abortPreparation(ctx, reservation, generationToken, "history_lookup_failed", now, err)
	}
	recentContentIDs := []string(nil)
	if studentContext.ClassStrategy != StrategyUniform {
		recentContentIDs, err = s.repo.ListRecentAttemptedContentIDs(ctx, studentID, recentAttemptExclusion)
		if err != nil {
			return TodayResponse{}, s.abortPreparation(ctx, reservation, generationToken, "recent_attempt_lookup_failed", now, err)
		}
	}

	choice, source, reason, found, err := s.chooseTeacherContent(
		ctx, studentID, studentContext, target, assignmentDate, historicalBodies, recentContentIDs,
	)
	if err != nil {
		if errors.Is(err, ErrDuplicateQuestion) && studentContext.ClassStrategy == StrategyUniform {
			return s.failPreparation(ctx, reservation, generationToken, "teacher_question_repeated", now, streak)
		}
		return TodayResponse{}, s.abortPreparation(ctx, reservation, generationToken, "content_selection_failed", now, err)
	}
	if found {
		finished, err := s.repo.FinishPreparation(ctx, reservation.ID, generationToken, choice, source, reason, now)
		if err != nil {
			if errors.Is(err, ErrDuplicateQuestion) {
				return s.failPreparation(ctx, reservation, generationToken, "question_repeated_during_assignment", now, streak)
			}
			return TodayResponse{}, s.abortPreparation(ctx, reservation, generationToken, "assignment_finalize_failed", now, err)
		}
		if !finished {
			return s.reloadAssignment(ctx, studentID, assignmentDate, streak)
		}
		s.reconcileUniformLowStock(ctx, studentContext, today, now)
		return s.reloadAssignment(ctx, studentID, assignmentDate, streak)
	}
	if studentContext.ClassStrategy == StrategyUniform && studentContext.ClassID != "" {
		return s.failPreparation(ctx, reservation, generationToken, FailureTeacherNotAssigned, now, streak)
	}

	if s.generator == nil {
		fallback, usedFallback, err := s.fallbackToRepeatedTeacherContent(
			ctx, reservation, generationToken, studentContext, target, recentContentIDs, now, streak,
		)
		if err != nil {
			return TodayResponse{}, s.abortPreparation(ctx, reservation, generationToken, "repeat_fallback_failed", now, err)
		}
		if usedFallback {
			return fallback, nil
		}
		return s.failPreparation(ctx, reservation, generationToken, "ai_generation_unavailable", now, streak)
	}
	if s.guard != nil {
		lease, err := s.guard.Acquire(ctx, studentID, "daily_question_prepare", "", false)
		if err != nil {
			fallback, usedFallback, fallbackErr := s.fallbackToRepeatedTeacherContent(
				ctx, reservation, generationToken, studentContext, target, recentContentIDs, now, streak,
			)
			if fallbackErr != nil {
				return TodayResponse{}, s.abortPreparation(ctx, reservation, generationToken, "repeat_fallback_failed", now, fallbackErr)
			}
			if usedFallback {
				return fallback, nil
			}
			return TodayResponse{}, s.abortPreparation(ctx, reservation, generationToken, "ai_access_unavailable", now, err)
		}
		defer releaseAILease(lease)
	}
	if s.limiter != nil && !s.limiter.Allow(ctx, studentID) {
		fallback, usedFallback, err := s.fallbackToRepeatedTeacherContent(
			ctx, reservation, generationToken, studentContext, target, recentContentIDs, now, streak,
		)
		if err != nil {
			return TodayResponse{}, s.abortPreparation(ctx, reservation, generationToken, "repeat_fallback_failed", now, err)
		}
		if usedFallback {
			return fallback, nil
		}
		response, err := s.failPreparation(ctx, reservation, generationToken, "ai_generation_rate_limited", now, streak)
		if err != nil {
			return TodayResponse{}, err
		}
		return response, ErrRateLimited
	}
	difficulty := normalizeDifficulty(studentContext.PreferredDifficulty)
	generated, err := s.generator.GenerateExercise(ctx, studentID, exerciseapp.GenerateExerciseRequest{
		ConceptID:           target.ConceptID,
		Difficulty:          difficulty,
		QuestionType:        exerciseapp.QuestionTypeMultipleChoice,
		AvoidQuestionBodies: historicalBodies,
	})
	if err != nil || generated == nil || strings.TrimSpace(generated.ID) == "" {
		fallback, usedFallback, fallbackErr := s.fallbackToRepeatedTeacherContent(
			ctx, reservation, generationToken, studentContext, target, recentContentIDs, now, streak,
		)
		if fallbackErr != nil {
			return TodayResponse{}, s.abortPreparation(ctx, reservation, generationToken, "repeat_fallback_failed", now, fallbackErr)
		}
		if usedFallback {
			return fallback, nil
		}
		return s.failPreparation(ctx, reservation, generationToken, generationFailureCode(err), now, streak)
	}
	choice = ContentChoice{
		ContentID:       generated.ID,
		TargetConceptID: target.ConceptID,
	}
	source = SourceAIGenerated
	reason = target.Reason
	finished, err := s.repo.FinishPreparation(ctx, reservation.ID, generationToken, choice, source, reason, now)
	if err != nil {
		if errors.Is(err, ErrDuplicateQuestion) {
			return s.failPreparation(ctx, reservation, generationToken, "question_repeated_during_assignment", now, streak)
		}
		return TodayResponse{}, s.abortPreparation(ctx, reservation, generationToken, "assignment_finalize_failed", now, err)
	}
	if !finished {
		return s.reloadAssignment(ctx, studentID, assignmentDate, streak)
	}
	return s.reloadAssignment(ctx, studentID, assignmentDate, streak)
}

func releaseAILease(lease airiskapp.Lease) {
	if lease == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = lease.Release(ctx)
}

// GetHistory returns a calendar month or a recent fixed-length history strip.
func (s *Service) GetHistory(ctx context.Context, studentID string, query HistoryQuery) (HistoryResponse, error) {
	today := shanghaiDay(s.now())
	start, end, days, err := normalizeHistoryQuery(query, today)
	if err != nil {
		return HistoryResponse{}, err
	}
	streak, err := s.streakDays(ctx, studentID, today)
	if err != nil {
		return HistoryResponse{}, err
	}
	assignments, err := s.repo.ListAssignments(ctx, studentID, start, end)
	if err != nil {
		return HistoryResponse{}, err
	}
	if days == 0 {
		items := make([]TodayResponse, 0, len(assignments))
		for _, assignment := range assignments {
			item := assignmentResponse(assignment, 0)
			item.Question = nil
			items = append(items, item)
		}
		return HistoryResponse{Items: items, StreakDays: streak}, nil
	}

	byDate := make(map[string]Assignment, len(assignments))
	for _, assignment := range assignments {
		byDate[dateString(assignment.AssignmentDate)] = assignment
	}
	items := make([]TodayResponse, 0, days)
	for cursor := today; !cursor.Before(start); cursor = cursor.AddDate(0, 0, -1) {
		if assignment, ok := byDate[dateString(cursor)]; ok {
			item := assignmentResponse(assignment, 0)
			item.Question = nil
			items = append(items, item)
			continue
		}
		items = append(items, notStartedResponse(cursor, 0))
	}
	return HistoryResponse{Items: items, StreakDays: streak}, nil
}

// GetDate returns an existing daily task for historical make-up work and
// resumes an abandoned generation attempt for that same persisted date.
func (s *Service) GetDate(ctx context.Context, studentID string, value string) (TodayResponse, error) {
	rawNow := s.now()
	date, err := parseShanghaiDate(value)
	if err != nil {
		return TodayResponse{}, ErrInvalidDate
	}
	today := shanghaiDay(rawNow)
	if date.After(today) {
		return TodayResponse{}, ErrNotFound
	}
	streak, err := s.streakDays(ctx, studentID, today)
	if err != nil {
		return TodayResponse{}, err
	}
	assignment, found, err := s.repo.GetAssignment(ctx, studentID, date)
	if err != nil {
		return TodayResponse{}, err
	}
	if !found {
		return TodayResponse{}, ErrNotFound
	}
	if assignment.Status == StatusUnavailable ||
		(assignment.Status == StatusReady && assignment.Question == nil) ||
		s.isPreparationStale(assignment, shanghaiNow(rawNow)) {
		return s.prepareDate(ctx, studentID, date, rawNow, 0, true, true)
	}
	if assignment.OpenedAt == nil && (assignment.Status == StatusReady || assignment.Status == StatusCompleted) {
		opened, ok, err := s.repo.MarkOpened(ctx, studentID, date, shanghaiNow(rawNow))
		if err != nil {
			return TodayResponse{}, err
		}
		if ok {
			assignment = opened
		}
	}
	return assignmentResponse(assignment, streak), nil
}

// GetClassSettings returns the strategy only when the requesting teacher owns the class.
func (s *Service) GetClassSettings(ctx context.Context, teacherID, classID string) (ClassSettings, error) {
	settings, found, err := s.repo.GetClassSettings(
		ctx, teacherID, strings.TrimSpace(classID), shanghaiDay(s.now()),
	)
	if err != nil {
		return ClassSettings{}, err
	}
	if !found {
		return ClassSettings{}, ErrNotFound
	}
	return settings, nil
}

// SetClassSettings changes either the selection strategy or the WeChat-only
// automatic reminder setting. At least one update must be supplied.
func (s *Service) SetClassSettings(
	ctx context.Context,
	teacherID string,
	classID string,
	strategy *string,
	autoReminderEnabled *bool,
) (ClassSettings, error) {
	classID = strings.TrimSpace(classID)
	if classID == "" || (strategy == nil && autoReminderEnabled == nil) {
		return ClassSettings{}, ErrInvalidStrategy
	}
	if autoReminderEnabled != nil && *autoReminderEnabled && !s.remindersEnabled {
		return ClassSettings{}, ErrReminderUnavailable
	}

	var normalizedStrategy *string
	if strategy != nil {
		value := strings.ToLower(strings.TrimSpace(*strategy))
		if value != StrategyPersonalized && value != StrategyUniform {
			return ClassSettings{}, ErrInvalidStrategy
		}
		normalizedStrategy = &value
	}

	rawNow := s.now()
	today := shanghaiDay(rawNow)
	result, found, err := s.repo.UpsertClassSettings(ctx, ClassSettingsUpdate{
		ClassID:             classID,
		TeacherID:           teacherID,
		Strategy:            normalizedStrategy,
		AutoReminderEnabled: autoReminderEnabled,
	}, today, shanghaiNow(rawNow))
	if err != nil {
		return ClassSettings{}, err
	}
	if !found {
		return ClassSettings{}, ErrNotFound
	}

	// The setting is the durable source of truth. Reminder reconciliation is
	// intentionally compensating: a queue failure must not make a committed
	// teacher choice look as though it failed to save.
	updated := result.Settings
	if result.AutoReminderJustEnabled {
		reminder, err := s.createClassReminder(ctx, teacherID, classID, today, ReminderKindAutomaticStudent, rawNow)
		if err != nil {
			slog.Error("daily question immediate reminder reconciliation failed", "error_code", "repository_error")
		} else if reminder.RecipientCount > 0 {
			updated.TodayReminderSent = true
			updated.TodayReminderRecipientCount += reminder.RecipientCount
		}
	}
	if s.remindersEnabled {
		if err := s.repo.EnsureUniformLowStockAlert(ctx, teacherID, classID, today, shanghaiNow(rawNow)); err != nil {
			slog.Error("daily question low-stock reconciliation failed", "error_code", "repository_error")
		}
	}
	return updated, nil
}

// GetClassUniformSchedule returns up to sixty explicit uniform questions from today.
func (s *Service) GetClassUniformSchedule(ctx context.Context, teacherID, classID string) (ClassUniformSchedule, error) {
	classID = strings.TrimSpace(classID)
	if classID == "" {
		return ClassUniformSchedule{}, ErrNotFound
	}
	rawNow := s.now()
	today := shanghaiDay(rawNow)
	schedule, found, err := s.repo.GetClassUniformSchedule(
		ctx,
		teacherID,
		classID,
		today,
		MaxUniformScheduleItems,
	)
	if err != nil {
		return ClassUniformSchedule{}, err
	}
	if !found {
		return ClassUniformSchedule{}, ErrNotFound
	}
	if s.remindersEnabled {
		if err := s.repo.EnsureUniformLowStockAlert(ctx, teacherID, classID, today, shanghaiNow(rawNow)); err != nil && ctx.Err() == nil {
			slog.Error("daily question low-stock reconciliation after schedule read failed", "error_code", "repository_error")
		}
	}
	return schedule, nil
}

// ReplaceClassUniformSchedule maps the supplied order to consecutive Shanghai dates.
// Dates already obtained by at least one student must remain at the same position.
func (s *Service) ReplaceClassUniformSchedule(ctx context.Context, teacherID, classID string, scheduleVersion int64, contentIDs []string) (ClassUniformSchedule, error) {
	classID = strings.TrimSpace(classID)
	if classID == "" || scheduleVersion < 0 || len(contentIDs) > MaxUniformScheduleItems {
		return ClassUniformSchedule{}, ErrInvalidContent
	}
	normalized := make([]string, 0, len(contentIDs))
	seen := make(map[string]struct{}, len(contentIDs))
	for _, contentID := range contentIDs {
		contentID = strings.TrimSpace(contentID)
		if !ValidIdentifier(contentID) {
			return ClassUniformSchedule{}, ErrInvalidContent
		}
		if _, exists := seen[contentID]; exists {
			return ClassUniformSchedule{}, ErrDuplicateQuestion
		}
		seen[contentID] = struct{}{}
		normalized = append(normalized, contentID)
	}
	rawNow := s.now()
	today := shanghaiDay(rawNow)
	schedule, found, err := s.repo.ReplaceClassUniformSchedule(ctx, UniformScheduleInput{
		TeacherID:       teacherID,
		ClassID:         classID,
		Today:           today,
		ScheduleVersion: scheduleVersion,
		ContentIDs:      normalized,
		Now:             shanghaiNow(rawNow),
	})
	if err != nil {
		return ClassUniformSchedule{}, err
	}
	if !found {
		return ClassUniformSchedule{}, ErrNotFound
	}
	if s.remindersEnabled {
		if err := s.repo.EnsureUniformLowStockAlert(ctx, teacherID, classID, today, shanghaiNow(rawNow)); err != nil && ctx.Err() == nil {
			slog.Error("daily question low-stock reconciliation after schedule update failed", "error_code", "repository_error")
		}
	}
	return schedule, nil
}

// GetClassStatistics returns completion and correction metrics without a personalized ranking.
func (s *Service) GetClassStatistics(ctx context.Context, teacherID, classID, dateValue string) (ClassStatistics, error) {
	date, err := dateOrToday(dateValue, s.now())
	if err != nil {
		return ClassStatistics{}, ErrInvalidDate
	}
	statistics, found, err := s.repo.GetClassStatistics(ctx, teacherID, strings.TrimSpace(classID), date)
	if err != nil {
		return ClassStatistics{}, err
	}
	if !found {
		return ClassStatistics{}, ErrNotFound
	}
	statistics.AssignmentDate = dateString(date)
	return statistics, nil
}

// SendClassReminder emits a new WeChat-only reminder to currently unfinished students.
func (s *Service) SendClassReminder(ctx context.Context, teacherID, classID, dateValue string) (ReminderResult, error) {
	if !s.remindersEnabled {
		return ReminderResult{}, ErrReminderUnavailable
	}
	now := s.now()
	date, err := dateOrToday(dateValue, now)
	if err != nil {
		return ReminderResult{}, ErrInvalidDate
	}
	if !date.Equal(shanghaiDay(now)) {
		return ReminderResult{}, ErrInvalidDate
	}
	classID = strings.TrimSpace(classID)
	settings, found, err := s.repo.GetClassSettings(ctx, teacherID, classID, date)
	if err != nil {
		return ReminderResult{}, err
	}
	if !found {
		return ReminderResult{}, ErrNotFound
	}
	if settings.EffectiveStrategy == StrategyUniform && !settings.UniformReady {
		return ReminderResult{}, ErrUniformQuestionNotAssigned
	}
	return s.createClassReminder(ctx, teacherID, classID, date, ReminderKindManualStudent, now)
}

// DispatchScheduledReminders is called by the 08:00 Shanghai worker. It is safe
// for more than one process to call because automatic event creation is durable
// and idempotent in PostgreSQL.
func (s *Service) DispatchScheduledReminders(ctx context.Context, now time.Time) error {
	if !s.remindersEnabled || !reminderTimeReached(now) {
		return nil
	}
	today := shanghaiDay(now)
	wallNow := shanghaiNow(now)
	autoErr := s.repo.DispatchAutomaticReminders(ctx, today, wallNow)
	lowStockErr := s.repo.DispatchUniformLowStockAlerts(ctx, today, wallNow)
	return errors.Join(autoErr, lowStockErr)
}

func (s *Service) createClassReminder(
	ctx context.Context,
	teacherID string,
	classID string,
	date time.Time,
	kind string,
	now time.Time,
) (ReminderResult, error) {
	reminderID, err := s.newID()
	if err != nil {
		return ReminderResult{}, fmt.Errorf("new daily reminder id: %w", err)
	}
	result, found, err := s.repo.CreateClassReminder(ctx, ClassReminderInput{
		ReminderID:     reminderID,
		TeacherID:      teacherID,
		ClassID:        classID,
		AssignmentDate: date,
		Kind:           kind,
		Now:            shanghaiNow(now),
	})
	if err != nil {
		return ReminderResult{}, err
	}
	if !found {
		return ReminderResult{}, ErrNotFound
	}
	return result, nil
}

// reconcileUniformLowStock is best effort so an already assigned student task
// never becomes an error merely because the notification queue is unavailable.
// The daily scheduler performs the durable follow-up reconciliation.
func (s *Service) reconcileUniformLowStock(
	ctx context.Context,
	student StudentContext,
	date time.Time,
	now time.Time,
) {
	if !s.remindersEnabled || student.ClassStrategy != StrategyUniform ||
		student.ClassID == "" || student.TeacherID == "" {
		return
	}
	if err := s.repo.EnsureUniformLowStockAlert(ctx, student.TeacherID, student.ClassID, date, now); err != nil && ctx.Err() == nil {
		slog.Error("daily question uniform low-stock reconciliation failed", "error_code", "repository_error")
	}
}

func (s *Service) chooseTeacherContent(
	ctx context.Context,
	studentID string,
	student StudentContext,
	target TargetSelection,
	date time.Time,
	historicalBodies []string,
	recentContentIDs []string,
) (ContentChoice, string, string, bool, error) {
	if student.ClassID == "" || student.TeacherID == "" {
		return ContentChoice{}, "", "", false, nil
	}
	if student.ClassStrategy == StrategyUniform {
		selection, found, err := s.repo.GetClassSelection(ctx, student.ClassID, date)
		if err != nil {
			return ContentChoice{}, "", "", false, err
		}
		if found && selection.ContentID != "" {
			if questiondedupe.IsDuplicate(selection.QuestionBody, historicalBodies) {
				return ContentChoice{}, "", "", false, ErrDuplicateQuestion
			}
			return ContentChoice{ContentID: selection.ContentID, TargetConceptID: selection.TargetConceptID}, selection.Source, selection.SelectionReason, true, nil
		}
		return ContentChoice{}, "", "", false, nil
	}

	choice, found, err := s.repo.FindTeacherContent(
		ctx, student.TeacherID, target.ConceptID, date, true, studentID, historicalBodies, recentContentIDs,
	)
	if err != nil {
		return ContentChoice{}, "", "", false, err
	}
	source := SourceTeacherCandidate
	if !found {
		choice, found, err = s.repo.FindTeacherContent(
			ctx, student.TeacherID, target.ConceptID, date, false, studentID, historicalBodies, recentContentIDs,
		)
		if err != nil {
			return ContentChoice{}, "", "", false, err
		}
		source = SourceTeacherBank
	}
	if !found {
		return ContentChoice{}, "", "", false, nil
	}
	reason := target.Reason
	if choice.TargetConceptID != target.ConceptID {
		reason = ReasonTeacherFallback
	}
	return choice, source, reason, true, nil
}

// chooseTeacherRepeatFallback is used only after the AI path cannot produce a new question.
func (s *Service) chooseTeacherRepeatFallback(
	ctx context.Context,
	studentID string,
	student StudentContext,
	target TargetSelection,
	date time.Time,
	recentContentIDs []string,
) (ContentChoice, string, bool, error) {
	if student.ClassID == "" || student.TeacherID == "" || student.ClassStrategy == StrategyUniform {
		return ContentChoice{}, "", false, nil
	}
	choice, found, err := s.repo.FindTeacherRepeatFallback(
		ctx, student.TeacherID, target.ConceptID, date, true, studentID, recentContentIDs,
	)
	if err != nil || found {
		return choice, SourceTeacherCandidate, found, err
	}
	choice, found, err = s.repo.FindTeacherRepeatFallback(
		ctx, student.TeacherID, target.ConceptID, date, false, studentID, recentContentIDs,
	)
	if err != nil {
		return ContentChoice{}, "", false, err
	}
	return choice, SourceTeacherBank, found, nil
}

func (s *Service) fallbackToRepeatedTeacherContent(
	ctx context.Context,
	assignment Assignment,
	generationToken string,
	student StudentContext,
	target TargetSelection,
	recentContentIDs []string,
	now time.Time,
	streak int,
) (TodayResponse, bool, error) {
	choice, source, found, err := s.chooseTeacherRepeatFallback(
		ctx,
		assignment.StudentID,
		student,
		target,
		assignment.AssignmentDate,
		recentContentIDs,
	)
	if err != nil || !found {
		return TodayResponse{}, false, err
	}
	finished, err := s.repo.FinishPreparation(
		ctx,
		assignment.ID,
		generationToken,
		choice,
		source,
		ReasonRepeatFallback,
		now,
	)
	if err != nil {
		return TodayResponse{}, false, err
	}
	if !finished {
		response, err := s.reloadAssignment(ctx, assignment.StudentID, assignment.AssignmentDate, streak)
		return response, true, err
	}
	response, err := s.reloadAssignment(ctx, assignment.StudentID, assignment.AssignmentDate, streak)
	return response, true, err
}

func (s *Service) failPreparation(ctx context.Context, assignment Assignment, token, failureCode string, now time.Time, streak int) (TodayResponse, error) {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := s.repo.FailPreparation(writeCtx, assignment.ID, token, failureCode, now); err != nil {
		return TodayResponse{}, err
	}
	return s.reloadAssignment(ctx, assignment.StudentID, assignment.AssignmentDate, streak)
}

func (s *Service) abortPreparation(
	ctx context.Context,
	assignment Assignment,
	token string,
	failureCode string,
	now time.Time,
	cause error,
) error {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := s.repo.FailPreparation(writeCtx, assignment.ID, token, failureCode, now); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func (s *Service) reloadAssignment(ctx context.Context, studentID string, date time.Time, streak int) (TodayResponse, error) {
	assignment, found, err := s.repo.GetAssignment(ctx, studentID, date)
	if err != nil {
		return TodayResponse{}, err
	}
	if !found {
		return notStartedResponse(date, streak), nil
	}
	return assignmentResponse(assignment, streak), nil
}

func (s *Service) isPreparationStale(assignment Assignment, now time.Time) bool {
	return assignment.Status == StatusPreparing &&
		!assignment.UpdatedAt.After(now.Add(-s.preparationStaleAfter))
}

func (s *Service) streakDays(ctx context.Context, studentID string, today time.Time) (int, error) {
	// Streaks are intentionally not capped by the history-page window.
	start := time.Date(1, time.January, 1, 0, 0, 0, 0, shanghaiLocation)
	dates, err := s.repo.ListStreakDates(ctx, studentID, start, today)
	if err != nil {
		return 0, err
	}
	active := make(map[string]struct{}, len(dates))
	for _, date := range dates {
		active[dateString(date)] = struct{}{}
	}
	cursor := today
	if _, ok := active[dateString(cursor)]; !ok {
		cursor = cursor.AddDate(0, 0, -1)
	}
	streak := 0
	for {
		if _, ok := active[dateString(cursor)]; !ok {
			return streak, nil
		}
		streak++
		cursor = cursor.AddDate(0, 0, -1)
	}
}

func assignmentResponse(assignment Assignment, streak int) TodayResponse {
	status := assignment.Status
	failureCode := cloneString(assignment.FailureCode)
	if status == StatusReady && assignment.Question == nil {
		status = StatusUnavailable
		if failureCode == nil {
			value := "content_unavailable"
			failureCode = &value
		}
	}
	return TodayResponse{
		Status:             status,
		AssignmentID:       stringPointer(assignment.ID),
		AssignmentDate:     dateString(assignment.AssignmentDate),
		ContentID:          cloneString(assignment.ContentID),
		TargetConceptID:    cloneString(assignment.TargetConceptID),
		TargetConceptName:  cloneString(assignment.TargetConceptName),
		Source:             stringPointer(assignment.Source),
		SelectionReason:    stringPointer(assignment.SelectionReason),
		FirstAttemptID:     cloneString(assignment.FirstAttemptID),
		CorrectedAttemptID: cloneString(assignment.CorrectedAttemptID),
		FirstResult:        cloneString(assignment.FirstResult),
		CountsTowardStreak: assignment.CountsTowardStreak,
		StreakDays:         streak,
		AssignedAt:         timePointer(assignment.AssignedAt),
		OpenedAt:           optionalTimePointer(assignment.OpenedAt),
		CompletedAt:        optionalTimePointer(assignment.CompletedAt),
		UpdatedAt:          timePointer(assignment.UpdatedAt),
		FailureCode:        failureCode,
		Question:           assignment.Question,
		RetryCount:         assignment.RetryCount,
	}
}

func notStartedResponse(date time.Time, streak int) TodayResponse {
	return TodayResponse{Status: StatusNotStarted, AssignmentDate: dateString(date), StreakDays: streak}
}

func teacherNotAssignedResponse(date time.Time, streak int) TodayResponse {
	failureCode := FailureTeacherNotAssigned
	return TodayResponse{
		Status:         StatusUnavailable,
		AssignmentDate: dateString(date),
		StreakDays:     streak,
		FailureCode:    &failureCode,
	}
}

func normalizeHistoryQuery(query HistoryQuery, today time.Time) (time.Time, time.Time, int, error) {
	if query.Days < 0 {
		return time.Time{}, time.Time{}, 0, ErrInvalidDays
	}
	if query.Days > 0 {
		if query.Days > maxHistoryDays {
			return time.Time{}, time.Time{}, 0, ErrInvalidDays
		}
		start := today.AddDate(0, 0, -(query.Days - 1))
		return start, today.AddDate(0, 0, 1), query.Days, nil
	}
	if strings.TrimSpace(query.Month) == "" {
		start := today.AddDate(0, 0, -(defaultHistoryDays - 1))
		return start, today.AddDate(0, 0, 1), defaultHistoryDays, nil
	}
	parsed, err := time.ParseInLocation("2006-01", strings.TrimSpace(query.Month), shanghaiLocation)
	if err != nil {
		return time.Time{}, time.Time{}, 0, ErrInvalidMonth
	}
	start := time.Date(parsed.Year(), parsed.Month(), 1, 0, 0, 0, 0, shanghaiLocation)
	if start.After(today) {
		return time.Time{}, time.Time{}, 0, ErrInvalidMonth
	}
	end := start.AddDate(0, 1, 0)
	if end.After(today.AddDate(0, 0, 1)) {
		end = today.AddDate(0, 0, 1)
	}
	return start, end, 0, nil
}

func parseShanghaiDate(value string) (time.Time, error) {
	parsed, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(value), shanghaiLocation)
	if err != nil {
		return time.Time{}, err
	}
	return shanghaiDay(parsed), nil
}

func dateOrToday(value string, now time.Time) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return shanghaiDay(now), nil
	}
	return parseShanghaiDate(value)
}

func shanghaiNow(value time.Time) time.Time {
	return value.In(shanghaiLocation)
}

func shanghaiDay(value time.Time) time.Time {
	value = value.In(shanghaiLocation)
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, shanghaiLocation)
}

func reminderTimeReached(value time.Time) bool {
	local := value.In(shanghaiLocation)
	runAt := time.Date(local.Year(), local.Month(), local.Day(), 8, 0, 0, 0, shanghaiLocation)
	return !local.Before(runAt)
}

func dateString(value time.Time) string {
	return value.Format("2006-01-02")
}

func timePointer(value time.Time) *string {
	if value.IsZero() {
		return nil
	}
	formatted := value.Format("2006-01-02T15:04:05.999999")
	return &formatted
}

func optionalTimePointer(value *time.Time) *string {
	if value == nil {
		return nil
	}
	return timePointer(*value)
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func normalizeDifficulty(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value > 1 {
		return 0.5
	}
	return value
}

func generationFailureCode(err error) string {
	switch {
	case err == nil:
		return "ai_generation_invalid_response"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, exerciseapp.ErrAIGenerationTimeout), errors.Is(err, exerciseapp.ErrMathSolverTimeout):
		return "ai_generation_timeout"
	case errors.Is(err, exerciseapp.ErrMathSolverUnavailable):
		return "solver_unavailable"
	case errors.Is(err, exerciseapp.ErrGeneratedContentInvalid), errors.Is(err, exerciseapp.ErrMathSolverInvalidResult):
		return "ai_generation_invalid_content"
	default:
		return "ai_generation_unavailable"
	}
}

// ValidIdentifier is intentionally small and public for HTTP adapters that need
// to reject malformed route values before calling persistence.
func ValidIdentifier(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= 36 && !strings.ContainsRune(value, '\x00')
}
