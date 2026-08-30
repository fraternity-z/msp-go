package mistake

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"mathstudy/backend/internal/application/learningrange"
	"mathstudy/backend/internal/platform/maputil"
	"mathstudy/backend/internal/platform/metautil"
	"mathstudy/backend/internal/platform/numutil"
	"mathstudy/backend/internal/platform/ptrutil"
	"mathstudy/backend/internal/platform/sliceutil"
	"mathstudy/backend/internal/platform/timefmt"
)

var (
	// ErrNotFound is returned when a requested mistake record does not exist for the user.
	ErrNotFound = errors.New("mistake not found")
	// ErrProfileNotFound is returned when a mistake write needs a missing student profile.
	ErrProfileNotFound = errors.New("student profile not found")
	// ErrDailyAttemptLocked protects the immutable evidence behind a daily completion.
	ErrDailyAttemptLocked = errors.New("daily question attempts cannot be deleted")
	// ErrMasteryVerificationRequired prevents self-reported mastery from changing learning state.
	ErrMasteryVerificationRequired = errors.New("mastery verification required")
	// ErrReviewNotDue remains for compatibility with older review clients and handlers.
	ErrReviewNotDue = errors.New("mistake review is not due")
)

// Repository is the persistence surface required by mistake book use cases.
type Repository interface {
	WithTx(context.Context, func(context.Context, Repository) error) error
	LockStudentTracking(context.Context, string) error
	ListMistakes(context.Context, string, ListFilter) ([]MistakeRow, error)
	ListMistakePage(context.Context, string, ListQuery) ([]MistakeListRow, int, error)
	GetMistakeByAttempt(context.Context, string, string) (MistakeRow, bool, error)
	GetAttemptContent(context.Context, string, string) (AttemptContent, bool, error)
	ListAttemptHistory(context.Context, string, string, string) ([]AttemptHistoryRow, error)
	GetProfile(context.Context, string) (StudentProfile, bool, error)
	ErrorCountsByContent(context.Context, string) (map[string]int, error)
	KnowledgeNames(context.Context, []string) (map[string]string, error)
	CountSubmittedAttempts(context.Context, string, *time.Time, *time.Time) (int, error)
	UpdateProfileMastery(context.Context, string, map[string]float64, time.Time) (bool, error)
	DeleteAttempt(context.Context, string, string) (bool, error)
	ListReviewTasks(context.Context, string, ReviewTaskQuery) ([]ReviewTaskRow, int, error)
	CountReviewTasks(context.Context, string, time.Time) (ReviewTaskCounts, error)
	GetReviewTaskByAttempt(context.Context, string, string) (ReviewTaskAssociation, bool, error)
	ArchiveMistakeRecord(context.Context, string, string, time.Time) (bool, error)
}

// ReviewEligibility checks whether an owned mistake's exercise remains submittable.
type ReviewEligibility interface {
	CanSubmitReviewExercise(context.Context, string, string, string) (bool, error)
}

// ListFilter stores database-level mistake filters.
type ListFilter struct {
	ErrorType     string
	ConceptID     string
	DifficultyMin float64
	DifficultyMax float64
	DateFrom      *time.Time
	DateTo        *time.Time
}

// ListQuery stores the full /mistakes list query.
type ListQuery struct {
	Page          int
	PageSize      int
	Now           time.Time
	ErrorType     string
	ConceptID     string
	DifficultyMin float64
	DifficultyMax float64
	DateFrom      *time.Time
	DateTo        *time.Time
	MasteryStatus string
	// ReviewStatus optionally narrows the current review-plan state. It is
	// intentionally separate from DueStatus so callers can combine, for
	// example, a mastered-stage filter with a mastery filter without changing
	// the historical mistake aggregation.
	ReviewStatus string
	// DueStatus selects whether the associated plan is due, scheduled, or both.
	// "all" is the default for the mistake library.
	DueStatus string
	// Stage is the optional verification stage (0..3). Nil means all stages.
	Stage *int
	// ErrorCountMin filters aggregated cards by their historical wrong-answer
	// count. A value <= 0 disables the filter.
	ErrorCountMin int
	SortBy        string
	SortOrder     string
}

// MistakeRow combines an attempt, diagnosis, and content record.
type MistakeRow struct {
	Attempt   Attempt
	Content   Content
	Diagnosis Diagnosis
}

// MistakeListRow stores one SQL-paginated mistake row with list aggregates.
type MistakeListRow struct {
	Row                   MistakeRow
	AvgMastery            float64
	ErrorCount            int
	LastReviewedAt        *time.Time
	IsEarlyPractice       bool
	ReviewTaskID          string
	ReviewStatus          string
	ReviewRevision        *int64
	ReviewDueAt           *time.Time
	ReviewMasteredAt      *time.Time
	ReviewStage           *int
	ReviewCount           int
	SuccessfulReviewCount int
	ReviewLastOutcome     *bool
	ReviewLastReviewedAt  *time.Time
	ReviewIsDue           bool
	DailyCorrection       bool
}

// AttemptContent combines an attempt and content row for write use cases.
type AttemptContent struct {
	Attempt Attempt
	Content Content
}

// AttemptHistoryRow stores repository-level attempt history before API time formatting.
type AttemptHistoryRow struct {
	AttemptID   string
	SubmittedAt *time.Time
	IsCorrect   bool
	Score       float64
}

// Attempt stores student answer data.
type Attempt struct {
	ID                string
	ContentID         string
	DailyAssignmentID string
	CanReview         bool
	CanDelete         bool
	StudentAnswer     string
	StudentSteps      []string
	IsCorrect         bool
	Score             float64
	SubmittedAt       *time.Time
	TimeSpentSeconds  int
}

// Content stores exercise-like content fields used by the mistake book.
type Content struct {
	ID         string
	Type       string
	Title      string
	Body       string
	Difficulty float64
	ConceptIDs []string
	Meta       map[string]any
}

// Diagnosis stores diagnostic metadata for a mistake.
type Diagnosis struct {
	ErrorType         *string
	ErrorSubtype      string
	Severity          string
	Explanation       string
	Suggestion        string
	RelatedConceptIDs []string
	ErrorStepIndex    *int
}

// StudentProfile stores mastery data used by the mistake book.
type StudentProfile struct {
	MasteryVector map[string]float64
}

// MistakeListResponse is the Python-compatible GET /mistakes response.
type MistakeListResponse struct {
	Items      []MistakeItem     `json:"items"`
	Pagination PaginationInfo    `json:"pagination"`
	Statistics MistakeStatistics `json:"statistics"`
}

// MistakeItem stores one list row.
type MistakeItem struct {
	ID                    string           `json:"id"`
	Exercise              MistakeExercise  `json:"exercise"`
	Attempt               MistakeAttempt   `json:"attempt"`
	Diagnosis             MistakeDiagnosis `json:"diagnosis"`
	Mastery               MistakeMastery   `json:"mastery"`
	ErrorCount            int              `json:"error_count"`
	LastReviewedAt        *string          `json:"last_reviewed_at"`
	CanReview             bool             `json:"can_review"`
	CanDelete             bool             `json:"can_delete"`
	CanArchive            bool             `json:"can_archive"`
	IsEarlyPractice       bool             `json:"is_early_practice"`
	ReviewTaskID          string           `json:"review_task_id,omitempty"`
	ReviewStatus          string           `json:"review_status,omitempty"`
	ReviewRevision        *int64           `json:"review_revision"`
	ReviewDueAt           *string          `json:"review_due_at"`
	ReviewStage           *int             `json:"review_stage"`
	ReviewCount           int              `json:"review_count"`
	SuccessfulReviewCount int              `json:"successful_review_count"`
	MasteredAt            *string          `json:"mastered_at"`
	ReviewLastOutcome     *bool            `json:"review_last_outcome"`
	ReviewLastReviewedAt  *string          `json:"review_last_reviewed_at"`
	ReviewIsDue           bool             `json:"review_is_due"`
	DailyCorrection       bool             `json:"daily_correction"`
}

// MistakeExercise stores exercise summary data for a mistake row.
type MistakeExercise struct {
	ID                  string   `json:"id"`
	Title               string   `json:"title"`
	Content             string   `json:"content"`
	Difficulty          float64  `json:"difficulty"`
	KnowledgePoints     []string `json:"knowledge_points"`
	KnowledgePointNames []string `json:"knowledge_point_names"`
}

// MistakeAttempt stores answer summary data for a mistake row.
type MistakeAttempt struct {
	StudentAnswer    string  `json:"student_answer"`
	CorrectAnswer    string  `json:"correct_answer"`
	IsCorrect        bool    `json:"is_correct"`
	Score            float64 `json:"score"`
	SubmittedAt      *string `json:"submitted_at"`
	TimeSpentSeconds int     `json:"time_spent_seconds"`
}

// MistakeDiagnosis stores diagnosis summary data.
type MistakeDiagnosis struct {
	ErrorType       *string  `json:"error_type"`
	ErrorSubtype    string   `json:"error_subtype"`
	Severity        string   `json:"severity"`
	Explanation     string   `json:"explanation"`
	Suggestion      string   `json:"suggestion"`
	RelatedConcepts []string `json:"related_concepts"`
}

// MistakeMastery stores mastery state for a mistake row.
type MistakeMastery struct {
	Current  float64 `json:"current"`
	Previous float64 `json:"previous"`
	Trend    string  `json:"trend"`
}

// PaginationInfo stores page metadata.
type PaginationInfo struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

// MistakeStatistics stores list-level summary data.
type MistakeStatistics struct {
	TotalMistakes int     `json:"total_mistakes"`
	WeakConcepts  int     `json:"weak_concepts"`
	AvgMastery    float64 `json:"avg_mastery"`
}

// StatisticsResponse is the Python-compatible GET /mistakes/statistics response.
type StatisticsResponse struct {
	Overview              StatisticsOverview               `json:"overview"`
	ErrorTypeDistribution map[string]ErrorTypeDistribution `json:"error_type_distribution"`
	ConceptWeakness       []ConceptWeakness                `json:"concept_weakness"`
}

// StatisticsOverview stores mistake summary counters.
type StatisticsOverview struct {
	TotalMistakes  int     `json:"total_mistakes"`
	TotalExercises int     `json:"total_exercises"`
	MistakeRate    float64 `json:"mistake_rate"`
	AvgMastery     float64 `json:"avg_mastery"`
}

// ErrorTypeDistribution stores an error type bucket.
type ErrorTypeDistribution struct {
	Count      int     `json:"count"`
	Percentage float64 `json:"percentage"`
	Label      string  `json:"label"`
}

// ConceptWeakness stores mistake count and mastery for one concept.
type ConceptWeakness struct {
	ConceptID      string  `json:"concept_id"`
	ConceptName    string  `json:"concept_name"`
	MistakeCount   int     `json:"mistake_count"`
	Mastery        float64 `json:"mastery"`
	RecentMistakes int     `json:"recent_mistakes"`
}

// DetailResponse is the Python-compatible GET /mistakes/{attempt_id} response.
type DetailResponse struct {
	AttemptID string                 `json:"attempt_id"`
	Exercise  MistakeDetailExercise  `json:"exercise"`
	Attempt   MistakeDetailAttempt   `json:"attempt"`
	Diagnosis MistakeDetailDiagnosis `json:"diagnosis"`
	Solution  MistakeSolution        `json:"solution"`
	History   []MistakeHistory       `json:"history"`
}

// MistakeDetailExercise stores detailed exercise data.
type MistakeDetailExercise struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Content         string   `json:"content"`
	Difficulty      float64  `json:"difficulty"`
	KnowledgePoints []string `json:"knowledge_points"`
	Hints           []string `json:"hints"`
}

// MistakeDetailAttempt stores detailed answer data.
type MistakeDetailAttempt struct {
	StudentAnswer    string   `json:"student_answer"`
	StudentSteps     []string `json:"student_steps"`
	CorrectAnswer    string   `json:"correct_answer"`
	SubmittedAt      *string  `json:"submitted_at"`
	TimeSpentSeconds int      `json:"time_spent_seconds"`
}

// MistakeDetailDiagnosis stores detailed diagnostic data.
type MistakeDetailDiagnosis struct {
	ErrorType       *string  `json:"error_type"`
	ErrorStepIndex  *int     `json:"error_step_index"`
	Explanation     string   `json:"explanation"`
	Suggestion      string   `json:"suggestion"`
	RelatedConcepts []string `json:"related_concepts"`
}

// MistakeSolution stores cached solution data.
type MistakeSolution struct {
	Answer string   `json:"answer"`
	Steps  []string `json:"steps"`
	Source string   `json:"source"`
}

// MistakeHistory stores prior attempts for the same content.
type MistakeHistory struct {
	AttemptID   string  `json:"attempt_id"`
	SubmittedAt *string `json:"submitted_at"`
	IsCorrect   bool    `json:"is_correct"`
	Score       float64 `json:"score"`
}

// MarkAsMasteredResponse is the Python-compatible POST /mistakes/{attempt_id}/master response.
type MarkAsMasteredResponse struct {
	Success       bool               `json:"success"`
	MasteredAt    string             `json:"mastered_at,omitempty"`
	MasteryUpdate map[string]float64 `json:"mastery_update,omitempty"`
}

// ReviewExerciseResponse is the Python-compatible GET /mistakes/review/next response.
type ReviewExerciseResponse struct {
	Exercise ReviewExercise `json:"exercise"`
	Context  ReviewContext  `json:"context"`
}

// ReviewExercise stores recommended exercise data.
type ReviewExercise struct {
	ID                   string   `json:"id"`
	Title                string   `json:"title"`
	Content              string   `json:"content"`
	Difficulty           float64  `json:"difficulty"`
	Type                 string   `json:"type"`
	KnowledgePoints      []string `json:"knowledge_points"`
	KnowledgePointNames  []string `json:"knowledge_point_names"`
	HintsAvailable       bool     `json:"hints_available"`
	EstimatedTimeSeconds int      `json:"estimated_time_seconds"`
	Options              []string `json:"options"`
}

// ReviewContext stores context for the recommended review item.
type ReviewContext struct {
	IsReview            bool    `json:"is_review"`
	OriginalAttemptID   string  `json:"original_attempt_id"`
	ReviewTaskID        string  `json:"review_task_id,omitempty"`
	ReviewTaskRevision  *int64  `json:"review_task_revision,omitempty"`
	DailyAssignmentID   string  `json:"daily_assignment_id,omitempty"`
	PreviousAnswer      string  `json:"previous_answer"`
	PreviousErrorType   *string `json:"previous_error_type"`
	PreviousExplanation string  `json:"previous_explanation"`
	PreviousSuggestion  string  `json:"previous_suggestion"`
	MasteryBefore       float64 `json:"mastery_before"`
	ErrorCount          int     `json:"error_count"`
}

// DeleteResponse is the Python-compatible DELETE /mistakes/{attempt_id} response.
type DeleteResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// Service implements mistake book use cases.
type Service struct {
	repo              Repository
	reviewEligibility ReviewEligibility
	now               func() time.Time
}

// NewService creates a mistake book service.
func NewService(repo Repository, reviewEligibility ReviewEligibility) (*Service, error) {
	if repo == nil {
		return nil, errors.New("mistake repository is nil")
	}
	if reviewEligibility == nil {
		return nil, errors.New("mistake review eligibility is nil")
	}
	return &Service{repo: repo, reviewEligibility: reviewEligibility, now: time.Now}, nil
}

// GetMistakes returns paginated mistakes with filtering and sorting.
func (s *Service) GetMistakes(ctx context.Context, userID string, query ListQuery) (MistakeListResponse, error) {
	query = normalizeListQuery(query)
	query.Now = s.now().UTC()
	rows, total, err := s.repo.ListMistakePage(ctx, userID, query)
	if err != nil {
		return MistakeListResponse{}, err
	}
	profile, _, err := s.repo.GetProfile(ctx, userID)
	if err != nil {
		return MistakeListResponse{}, err
	}
	mastery := maputil.CloneFloatMap(profile.MasteryVector)

	responseItems := make([]MistakeItem, 0, len(rows))
	knowledgeNames, err := s.knowledgeNames(ctx, contentsFromMistakeListRows(rows))
	if err != nil {
		return MistakeListResponse{}, err
	}
	for _, row := range rows {
		responseItems = append(responseItems, toMistakeItem(listItemData{
			row:                   row.Row,
			avgMastery:            row.AvgMastery,
			errorCount:            row.ErrorCount,
			lastReviewedAt:        row.LastReviewedAt,
			isEarlyPractice:       row.IsEarlyPractice,
			reviewTaskID:          row.ReviewTaskID,
			reviewStatus:          row.ReviewStatus,
			reviewRevision:        row.ReviewRevision,
			reviewDueAt:           row.ReviewDueAt,
			reviewMasteredAt:      row.ReviewMasteredAt,
			reviewStage:           row.ReviewStage,
			reviewCount:           row.ReviewCount,
			successfulReviewCount: row.SuccessfulReviewCount,
			reviewLastOutcome:     row.ReviewLastOutcome,
			reviewLastReviewedAt:  row.ReviewLastReviewedAt,
			reviewIsDue:           row.ReviewIsDue,
			dailyCorrection:       row.DailyCorrection,
			knowledgeNames:        knowledgeNames,
		}))
	}

	return MistakeListResponse{
		Items: responseItems,
		Pagination: PaginationInfo{
			Page:       query.Page,
			PageSize:   query.PageSize,
			Total:      total,
			TotalPages: numutil.TotalPages(total, query.PageSize),
		},
		Statistics: MistakeStatistics{
			TotalMistakes: total,
			WeakConcepts:  countWeakConcepts(mastery),
			AvgMastery:    maputil.AverageFloatValues(mastery),
		},
	}, nil
}

// GetStatistics returns mistake statistics for a time range.
func (s *Service) GetStatistics(ctx context.Context, userID string, timeRange string) (StatisticsResponse, error) {
	start, end := s.timeRange(timeRange)
	rows, err := s.repo.ListMistakes(ctx, userID, ListFilter{DifficultyMin: 0, DifficultyMax: 1, DateFrom: start, DateTo: end})
	if err != nil {
		return StatisticsResponse{}, err
	}
	profile, _, err := s.repo.GetProfile(ctx, userID)
	if err != nil {
		return StatisticsResponse{}, err
	}
	mastery := maputil.CloneFloatMap(profile.MasteryVector)
	totalExercises, err := s.repo.CountSubmittedAttempts(ctx, userID, start, end)
	if err != nil {
		return StatisticsResponse{}, err
	}

	errorCounts := map[string]int{}
	conceptMistakes := map[string]int{}
	for _, row := range rows {
		if row.Diagnosis.ErrorType != nil {
			errorCounts[*row.Diagnosis.ErrorType]++
		}
		for _, conceptID := range row.Content.ConceptIDs {
			conceptMistakes[conceptID]++
		}
	}

	totalMistakes := len(rows)
	return StatisticsResponse{
		Overview: StatisticsOverview{
			TotalMistakes:  totalMistakes,
			TotalExercises: totalExercises,
			MistakeRate:    numutil.RoundPlaces(numutil.Percent(totalExercises, totalMistakes), 1),
			AvgMastery:     numutil.RoundPlaces(maputil.AverageFloatValues(mastery), 2),
		},
		ErrorTypeDistribution: buildErrorTypeDistribution(errorCounts, totalMistakes),
		ConceptWeakness:       buildConceptWeakness(conceptMistakes, mastery),
	}, nil
}

// GetMistakeDetail returns detailed data for a mistake.
func (s *Service) GetMistakeDetail(ctx context.Context, userID string, attemptID string) (DetailResponse, error) {
	row, ok, err := s.repo.GetMistakeByAttempt(ctx, userID, attemptID)
	if err != nil {
		return DetailResponse{}, err
	}
	if !ok {
		return DetailResponse{}, ErrNotFound
	}
	history, err := s.repo.ListAttemptHistory(ctx, userID, row.Content.ID, attemptID)
	if err != nil {
		return DetailResponse{}, err
	}
	return toDetailResponse(row, history), nil
}

// MarkAsMastered is a compatibility endpoint that only confirms already verified mastery.
func (s *Service) MarkAsMastered(ctx context.Context, userID string, attemptID string) (MarkAsMasteredResponse, error) {
	task, ok, err := s.repo.GetReviewTaskByAttempt(ctx, userID, strings.TrimSpace(attemptID))
	if err != nil {
		return MarkAsMasteredResponse{}, err
	}
	if !ok {
		return MarkAsMasteredResponse{}, ErrNotFound
	}
	if task.Status != ReviewTaskMastered || task.MasteredAt == nil {
		return MarkAsMasteredResponse{}, ErrMasteryVerificationRequired
	}
	masteredAt := optionalAttemptTimestamp(task.MasteredAt)
	if masteredAt == nil {
		return MarkAsMasteredResponse{}, ErrMasteryVerificationRequired
	}
	return MarkAsMasteredResponse{
		Success:       true,
		MasteredAt:    *masteredAt,
		MasteryUpdate: map[string]float64{},
	}, nil
}

// DeleteMistake archives a list record while preserving its immutable answer evidence.
func (s *Service) DeleteMistake(ctx context.Context, userID string, attemptID string) (DeleteResponse, error) {
	attemptID = strings.TrimSpace(attemptID)
	if attemptID == "" {
		return DeleteResponse{}, ErrNotFound
	}
	err := s.repo.WithTx(ctx, func(txCtx context.Context, repo Repository) error {
		if err := repo.LockStudentTracking(txCtx, userID); err != nil {
			return err
		}
		archived, err := repo.ArchiveMistakeRecord(txCtx, userID, attemptID, s.now().UTC())
		if err != nil {
			return err
		}
		if !archived {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return DeleteResponse{}, err
	}
	return DeleteResponse{Success: true, Message: "错题已归档，历史作答和诊断证据已保留"}, nil
}

// GetReviewExercise returns the earliest active review task that is currently due.
func (s *Service) GetReviewExercise(ctx context.Context, userID string, focusConcept string, focusErrorType string) (ReviewExerciseResponse, error) {
	query := normalizeReviewTaskQuery(ReviewTaskQuery{
		View:      ReviewTaskViewDue,
		Page:      1,
		PageSize:  1,
		Now:       s.now().UTC(),
		ConceptID: focusConcept,
		ErrorType: focusErrorType,
	})
	rows, _, err := s.repo.ListReviewTasks(ctx, userID, query)
	if err != nil {
		return ReviewExerciseResponse{}, err
	}
	if len(rows) == 0 {
		return ReviewExerciseResponse{}, ErrNotFound
	}
	knowledgeNames, err := s.knowledgeNames(ctx, []Content{rows[0].Content})
	if err != nil {
		return ReviewExerciseResponse{}, err
	}
	return toReviewTaskResponse(rows[0], knowledgeNames), nil
}

// GetReviewExerciseByAttempt returns the exact incorrect attempt selected by the student.
func (s *Service) GetReviewExerciseByAttempt(ctx context.Context, userID string, attemptID string) (ReviewExerciseResponse, error) {
	attemptID = strings.TrimSpace(attemptID)
	row, ok, err := s.repo.GetMistakeByAttempt(ctx, userID, attemptID)
	if err != nil {
		return ReviewExerciseResponse{}, err
	}
	if !ok || row.Attempt.IsCorrect || row.Attempt.SubmittedAt == nil {
		return ReviewExerciseResponse{}, ErrNotFound
	}
	task, hasTask, err := s.repo.GetReviewTaskByAttempt(ctx, userID, attemptID)
	if err != nil {
		return ReviewExerciseResponse{}, err
	}
	if hasTask && (task.Status == ReviewTaskArchived || task.SourceAttemptID != attemptID) {
		hasTask = false
	}
	now := s.now().UTC()
	if hasTask {
		view := ReviewTaskViewDue
		if task.Status == ReviewTaskMastered {
			view = ReviewTaskViewMastered
		}
		rows, _, listErr := s.repo.ListReviewTasks(ctx, userID, normalizeReviewTaskQuery(ReviewTaskQuery{
			View:     view,
			Page:     1,
			PageSize: 1,
			Now:      now,
			TaskID:   task.ID,
		}))
		if listErr != nil {
			return ReviewExerciseResponse{}, listErr
		}
		if len(rows) != 1 {
			return ReviewExerciseResponse{}, ErrNotFound
		}
		knowledgeNames, namesErr := s.knowledgeNames(ctx, []Content{rows[0].Content})
		if namesErr != nil {
			return ReviewExerciseResponse{}, namesErr
		}
		return toReviewTaskResponse(rows[0], knowledgeNames), nil
	}
	profile, _, err := s.repo.GetProfile(ctx, userID)
	if err != nil {
		return ReviewExerciseResponse{}, err
	}
	errorCounts, err := s.repo.ErrorCountsByContent(ctx, userID)
	if err != nil {
		return ReviewExerciseResponse{}, err
	}
	errorCount := errorCounts[row.Content.ID]
	if errorCount == 0 {
		errorCount = 1
	}
	response := toReviewResponse(reviewCandidate{
		row:            row,
		avgMastery:     averageMastery(row.Content.ConceptIDs, profile.MasteryVector),
		errorCount:     errorCount,
		knowledgeNames: nil,
	})
	knowledgeNames, err := s.knowledgeNames(ctx, []Content{row.Content})
	if err != nil {
		return ReviewExerciseResponse{}, err
	}
	response.Exercise.KnowledgePointNames = knowledgePointNames(row.Content, knowledgeNames)
	response.Context.DailyAssignmentID = ""
	return response, nil
}

func normalizeListQuery(query ListQuery) ListQuery {
	query.DateFrom = utcTimePointer(query.DateFrom)
	query.DateTo = utcTimePointer(query.DateTo)
	query.ReviewStatus = strings.ToLower(strings.TrimSpace(query.ReviewStatus))
	query.DueStatus = strings.ToLower(strings.TrimSpace(query.DueStatus))
	if query.DueStatus == "" {
		query.DueStatus = "all"
	}
	if query.DueStatus == "overdue" {
		query.DueStatus = "due"
	}
	if query.DueStatus == "upcoming" {
		query.DueStatus = "scheduled"
	}
	if query.DueStatus != "all" && query.DueStatus != "due" && query.DueStatus != "scheduled" {
		query.DueStatus = "all"
	}
	switch query.ReviewStatus {
	case "", "all", "pending", "verification_due", "mastered", "archived", "none", "due", "overdue", "scheduled":
	default:
		query.ReviewStatus = "all"
	}
	// Keep the older review_status aliases useful while exposing the simpler
	// due_status filter used by the list UI.
	if (query.ReviewStatus == "due" || query.ReviewStatus == "overdue") && query.DueStatus == "all" {
		query.DueStatus = "due"
		query.ReviewStatus = "all"
	}
	if query.ReviewStatus == "scheduled" && query.DueStatus == "all" {
		query.DueStatus = "scheduled"
		query.ReviewStatus = "all"
	}
	if query.Stage != nil && (*query.Stage < 0 || *query.Stage > 3) {
		query.Stage = nil
	}
	if query.ErrorCountMin < 0 {
		query.ErrorCountMin = 0
	}
	if query.Now.IsZero() {
		query.Now = time.Now().UTC()
	}
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 {
		query.PageSize = 20
	}
	if query.PageSize > 100 {
		query.PageSize = 100
	}
	if query.DifficultyMax == 0 {
		query.DifficultyMax = 1
	}
	if query.DifficultyMin < 0 {
		query.DifficultyMin = 0
	}
	if query.DifficultyMax > 1 {
		query.DifficultyMax = 1
	}
	if query.DifficultyMin > query.DifficultyMax {
		query.DifficultyMin, query.DifficultyMax = query.DifficultyMax, query.DifficultyMin
	}
	if strings.TrimSpace(query.MasteryStatus) == "" {
		query.MasteryStatus = "all"
	}
	if strings.TrimSpace(query.SortBy) == "" {
		query.SortBy = "time"
	}
	if strings.TrimSpace(query.SortOrder) == "" {
		query.SortOrder = "desc"
	}
	return query
}

func (s *Service) timeRange(value string) (*time.Time, *time.Time) {
	now := s.now().UTC()
	var start *time.Time
	switch value {
	case "week":
		value := now.AddDate(0, 0, -7)
		start = &value
	case "semester":
		value := now.AddDate(0, 0, -120)
		start = &value
	case "all":
		return nil, nil
	default:
		value := now.AddDate(0, 0, -30)
		start = &value
	}
	return start, &now
}

func toMistakeItem(item listItemData) MistakeItem {
	row := item.row
	return MistakeItem{
		ID: row.Attempt.ID,
		Exercise: MistakeExercise{
			ID:                  row.Content.ID,
			Title:               nonEmpty(row.Content.Title, "无标题"),
			Content:             row.Content.Body,
			Difficulty:          row.Content.Difficulty,
			KnowledgePoints:     sliceutil.CloneStrings(row.Content.ConceptIDs),
			KnowledgePointNames: knowledgePointNames(row.Content, item.knowledgeNames),
		},
		Attempt: MistakeAttempt{
			StudentAnswer:    row.Attempt.StudentAnswer,
			CorrectAnswer:    metautil.String(row.Content.Meta, "answer"),
			IsCorrect:        row.Attempt.IsCorrect,
			Score:            row.Attempt.Score,
			SubmittedAt:      optionalAttemptTimestamp(row.Attempt.SubmittedAt),
			TimeSpentSeconds: row.Attempt.TimeSpentSeconds,
		},
		Diagnosis: MistakeDiagnosis{
			ErrorType:       ptrutil.Clone(row.Diagnosis.ErrorType),
			ErrorSubtype:    row.Diagnosis.ErrorSubtype,
			Severity:        row.Diagnosis.Severity,
			Explanation:     row.Diagnosis.Explanation,
			Suggestion:      row.Diagnosis.Suggestion,
			RelatedConcepts: sliceutil.CloneStrings(row.Diagnosis.RelatedConceptIDs),
		},
		Mastery: MistakeMastery{
			Current:  item.avgMastery,
			Previous: item.avgMastery,
			Trend:    masteryTrend(item.avgMastery),
		},
		ErrorCount:            item.errorCount,
		LastReviewedAt:        optionalAttemptTimestamp(item.lastReviewedAt),
		CanReview:             true,
		CanDelete:             row.Attempt.CanDelete,
		CanArchive:            true,
		IsEarlyPractice:       item.isEarlyPractice,
		ReviewTaskID:          item.reviewTaskID,
		ReviewStatus:          item.reviewStatus,
		ReviewRevision:        item.reviewRevision,
		ReviewDueAt:           optionalAttemptTimestamp(item.reviewDueAt),
		ReviewStage:           item.reviewStage,
		ReviewCount:           item.reviewCount,
		SuccessfulReviewCount: item.successfulReviewCount,
		MasteredAt:            optionalAttemptTimestamp(item.reviewMasteredAt),
		ReviewLastOutcome:     ptrutil.Clone(item.reviewLastOutcome),
		ReviewLastReviewedAt:  optionalAttemptTimestamp(item.reviewLastReviewedAt),
		ReviewIsDue:           item.reviewIsDue,
		DailyCorrection:       item.dailyCorrection,
	}
}

func toDetailResponse(row MistakeRow, historyRows []AttemptHistoryRow) DetailResponse {
	solutionSteps := metautil.StringSlice(row.Content.Meta, "solution_steps")
	source := "unavailable"
	if len(solutionSteps) > 0 {
		source = "cached"
	}
	history := make([]MistakeHistory, 0, len(historyRows))
	for _, item := range historyRows {
		history = append(history, MistakeHistory{
			AttemptID:   item.AttemptID,
			SubmittedAt: optionalAttemptTimestamp(item.SubmittedAt),
			IsCorrect:   item.IsCorrect,
			Score:       item.Score,
		})
	}
	return DetailResponse{
		AttemptID: row.Attempt.ID,
		Exercise: MistakeDetailExercise{
			ID:              row.Content.ID,
			Title:           nonEmpty(row.Content.Title, "无标题"),
			Content:         row.Content.Body,
			Difficulty:      row.Content.Difficulty,
			KnowledgePoints: sliceutil.CloneStrings(row.Content.ConceptIDs),
			Hints:           metautil.StringSlice(row.Content.Meta, "hints"),
		},
		Attempt: MistakeDetailAttempt{
			StudentAnswer:    row.Attempt.StudentAnswer,
			StudentSteps:     sliceutil.CloneStrings(row.Attempt.StudentSteps),
			CorrectAnswer:    metautil.String(row.Content.Meta, "answer"),
			SubmittedAt:      optionalAttemptTimestamp(row.Attempt.SubmittedAt),
			TimeSpentSeconds: row.Attempt.TimeSpentSeconds,
		},
		Diagnosis: MistakeDetailDiagnosis{
			ErrorType:       ptrutil.Clone(row.Diagnosis.ErrorType),
			ErrorStepIndex:  ptrutil.Clone(row.Diagnosis.ErrorStepIndex),
			Explanation:     row.Diagnosis.Explanation,
			Suggestion:      row.Diagnosis.Suggestion,
			RelatedConcepts: sliceutil.CloneStrings(row.Diagnosis.RelatedConceptIDs),
		},
		Solution: MistakeSolution{
			Answer: metautil.String(row.Content.Meta, "answer"),
			Steps:  solutionSteps,
			Source: source,
		},
		History: history,
	}
}

func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	converted := value.UTC()
	return &converted
}

func optionalAttemptTimestamp(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := timefmt.DateTimeRFC3339(learningrange.InPlatformZone(*value))
	return &formatted
}

func toReviewResponse(candidate reviewCandidate) ReviewExerciseResponse {
	row := candidate.row
	return ReviewExerciseResponse{
		Exercise: ReviewExercise{
			ID:                   row.Content.ID,
			Title:                nonEmpty(row.Content.Title, "无标题"),
			Content:              row.Content.Body,
			Difficulty:           row.Content.Difficulty,
			Type:                 reviewQuestionType(row.Content),
			KnowledgePoints:      sliceutil.CloneStrings(row.Content.ConceptIDs),
			KnowledgePointNames:  knowledgePointNames(row.Content, candidate.knowledgeNames),
			HintsAvailable:       len(metautil.StringSlice(row.Content.Meta, "hints")) > 0,
			EstimatedTimeSeconds: metautil.IntDefault(row.Content.Meta, "estimated_time_seconds", 300),
			Options:              metautil.OptionalStringSlice(row.Content.Meta, "options"),
		},
		Context: ReviewContext{
			IsReview:            true,
			OriginalAttemptID:   row.Attempt.ID,
			DailyAssignmentID:   row.Attempt.DailyAssignmentID,
			PreviousAnswer:      row.Attempt.StudentAnswer,
			PreviousErrorType:   ptrutil.Clone(row.Diagnosis.ErrorType),
			PreviousExplanation: row.Diagnosis.Explanation,
			PreviousSuggestion:  row.Diagnosis.Suggestion,
			MasteryBefore:       candidate.avgMastery,
			ErrorCount:          candidate.errorCount,
		},
	}
}

func toReviewTaskResponse(row ReviewTaskRow, knowledgeNames map[string]string) ReviewExerciseResponse {
	response := toReviewResponse(reviewCandidate{
		row: MistakeRow{
			Attempt: Attempt{
				ID:            row.SourceAttemptID,
				StudentAnswer: row.SourceStudentAnswer,
			},
			Content:   row.Content,
			Diagnosis: row.Diagnosis,
		},
		avgMastery:     row.AvgMastery,
		errorCount:     row.ErrorCount,
		knowledgeNames: knowledgeNames,
	})
	response.Context.ReviewTaskID = row.Association.ID
	revision := row.Association.Revision
	response.Context.ReviewTaskRevision = &revision
	response.Context.DailyAssignmentID = ""
	return response
}

func buildErrorTypeDistribution(counts map[string]int, total int) map[string]ErrorTypeDistribution {
	labels := map[string]string{
		"conceptual":  "概念性错误",
		"procedural":  "过程性错误",
		"logical":     "逻辑错误",
		"symbolic":    "符号错误",
		"calculation": "计算错误",
	}
	distribution := map[string]ErrorTypeDistribution{}
	for key, count := range counts {
		percentage := 0.0
		if total > 0 {
			percentage = numutil.RoundPlaces(numutil.Percent(total, count), 1)
		}
		label := labels[key]
		if label == "" {
			label = "未知错误"
		}
		distribution[key] = ErrorTypeDistribution{Count: count, Percentage: percentage, Label: label}
	}
	return distribution
}

func buildConceptWeakness(counts map[string]int, mastery map[string]float64) []ConceptWeakness {
	items := make([]ConceptWeakness, 0, len(counts))
	for conceptID, count := range counts {
		items = append(items, ConceptWeakness{
			ConceptID:      conceptID,
			ConceptName:    conceptID,
			MistakeCount:   count,
			Mastery:        masteryValue(conceptID, mastery),
			RecentMistakes: count,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].MistakeCount == items[j].MistakeCount {
			return items[i].ConceptID < items[j].ConceptID
		}
		return items[i].MistakeCount > items[j].MistakeCount
	})
	if len(items) > 10 {
		return items[:10]
	}
	return items
}

func averageMastery(conceptIDs []string, mastery map[string]float64) float64 {
	if len(conceptIDs) == 0 {
		return 0.5
	}
	sum := 0.0
	for _, conceptID := range conceptIDs {
		sum += masteryValue(conceptID, mastery)
	}
	return sum / float64(len(conceptIDs))
}

func (s *Service) knowledgeNames(ctx context.Context, contents []Content) (map[string]string, error) {
	conceptIDs := make([]string, 0)
	for _, content := range contents {
		conceptIDs = sliceutil.AppendUniqueNonEmptyStrings(conceptIDs, content.ConceptIDs...)
	}
	return s.repo.KnowledgeNames(ctx, conceptIDs)
}

func contentsFromMistakeListRows(rows []MistakeListRow) []Content {
	contents := make([]Content, 0, len(rows))
	for _, row := range rows {
		contents = append(contents, row.Row.Content)
	}
	return contents
}

func contentsFromReviewTaskRows(rows []ReviewTaskRow) []Content {
	contents := make([]Content, 0, len(rows))
	for _, row := range rows {
		contents = append(contents, row.Content)
	}
	return contents
}

func knowledgePointNames(content Content, resolved map[string]string) []string {
	metaNames := metautil.StringSlice(content.Meta, "knowledge_point_names")
	names := make([]string, 0, len(content.ConceptIDs))
	for index, conceptID := range content.ConceptIDs {
		name := strings.TrimSpace(resolved[conceptID])
		if name == "" && index < len(metaNames) {
			name = strings.TrimSpace(metaNames[index])
		}
		if name == "" {
			name = conceptID
		}
		names = append(names, name)
	}
	return names
}

func masteryValue(conceptID string, mastery map[string]float64) float64 {
	value, ok := mastery[conceptID]
	if !ok {
		return 0.5
	}
	return value
}

func masteryTrend(avgMastery float64) string {
	if avgMastery < 0.4 {
		return "declining"
	}
	if avgMastery >= 0.7 {
		return "improving"
	}
	return "stable"
}

func countWeakConcepts(mastery map[string]float64) int {
	total := 0
	for _, value := range mastery {
		if value < 0.4 {
			total++
		}
	}
	return total
}

func reviewQuestionType(content Content) string {
	questionType := strings.ToLower(strings.TrimSpace(metautil.String(content.Meta, "type")))
	switch questionType {
	case "multiple_choice", "short_answer", "proof":
		return questionType
	default:
		return "short_answer"
	}
}

func nonEmpty(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

type listItemData struct {
	row                   MistakeRow
	avgMastery            float64
	errorCount            int
	lastReviewedAt        *time.Time
	isEarlyPractice       bool
	reviewTaskID          string
	reviewStatus          string
	reviewRevision        *int64
	reviewDueAt           *time.Time
	reviewMasteredAt      *time.Time
	reviewStage           *int
	reviewCount           int
	successfulReviewCount int
	reviewLastOutcome     *bool
	reviewLastReviewedAt  *time.Time
	reviewIsDue           bool
	dailyCorrection       bool
	knowledgeNames        map[string]string
}

type reviewCandidate struct {
	row            MistakeRow
	avgMastery     float64
	errorCount     int
	knowledgeNames map[string]string
}
