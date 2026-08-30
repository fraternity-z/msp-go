package airisk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
)

// Acquire checks student access, content, quota, and distributed concurrency.
// Metered requests reserve quota capacity until their lease is released.
func (s *Service) Acquire(ctx context.Context, studentID, source, content string, metered bool) (Lease, error) {
	access, ok, err := s.repo.GetStudentAccess(ctx, strings.TrimSpace(studentID))
	if err != nil {
		return nil, Error{Kind: ErrUnavailable, Message: "AI 风控服务暂不可用"}
	}
	if !ok || !access.IsStudent {
		return noopLease{}, nil
	}
	if access.IsBlocked {
		message := "你的 AI 使用权限已被管理员暂停"
		if access.BlockedReason != "" {
			message += "：" + access.BlockedReason
		}
		return nil, Error{Kind: ErrAccessBlocked, Message: message}
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, Error{Kind: ErrUnavailable, Message: "AI 风控服务暂不可用"}
	}
	if matched := matchBlockedKeyword(content, settings.BlockedKeywords); matched != "" {
		recordErr := s.recordContentBlock(ctx, access, source, content, matched)
		blocked := Error{Kind: ErrContentBlocked, Message: "该内容触发平台安全规则，请调整后重试"}
		if recordErr != nil {
			return nil, errors.Join(
				Error{Kind: ErrUnavailable, Message: "AI 风控服务暂不可用"},
				recordErr,
			)
		}
		return nil, blocked
	}
	usedToday := 0
	if metered {
		usedToday, err = s.repo.CountReplies(ctx, access.StudentID, s.usageDate(s.now()))
		if err != nil {
			return nil, Error{Kind: ErrUnavailable, Message: "AI 风控服务暂不可用"}
		}
		if usedToday >= settings.DailyReplyLimit {
			return nil, Error{Kind: ErrQuotaExceeded, Message: "今日 AI 回复额度已用完，请明天再试"}
		}
	}
	leaseID, err := s.newID()
	if err != nil {
		return nil, Error{Kind: ErrUnavailable, Message: "AI 风控服务暂不可用"}
	}
	dailyLimit := 0
	if metered {
		dailyLimit = settings.DailyReplyLimit
	}
	decision, err := s.slots.Acquire(
		ctx,
		access.StudentID,
		leaseID,
		settings.MaxConcurrentRequests,
		dailyLimit,
		usedToday,
		s.leaseTTL,
	)
	if err != nil {
		return nil, Error{Kind: ErrUnavailable, Message: "AI 风控服务暂不可用"}
	}
	if !decision.Allowed {
		if decision.Reason == "quota" {
			return nil, Error{Kind: ErrQuotaExceeded, Message: "今日 AI 回复额度已用完，请明天再试"}
		}
		return nil, Error{Kind: ErrConcurrencyExceeded, Message: "已有 AI 请求处理中，请等待完成后再试"}
	}
	lease := &distributedLease{store: s.slots, studentID: access.StudentID, leaseID: leaseID}
	if settings.ModelReviewEnabled && strings.TrimSpace(content) != "" {
		if err := s.enforceModelReview(ctx, access, source, content, settings.ModelReviewThresholds); err != nil {
			if releaseErr := lease.Release(context.WithoutCancel(ctx)); releaseErr != nil {
				return nil, errors.Join(
					Error{Kind: ErrUnavailable, Message: "AI 风控服务暂不可用"},
					fmt.Errorf("release AI lease after model review: %w", releaseErr),
				)
			}
			return nil, err
		}
	}
	return lease, nil
}

func (s *Service) enforceModelReview(
	ctx context.Context,
	access StudentAccess,
	source string,
	content string,
	thresholds map[string]float64,
) error {
	startedAt := time.Now()
	if s.reviewer == nil {
		return s.modelReviewUnavailable(ctx, access, source, content, ModelReviewResult{}, 0, errors.New("content reviewer is not configured"))
	}
	result, err := s.reviewer.Review(ctx, content)
	latencyMS := elapsedMilliseconds(startedAt)
	if err != nil {
		return s.modelReviewUnavailable(ctx, access, source, content, result, latencyMS, err)
	}
	blocked, category, score, err := evaluateModelReview(result, thresholds)
	if err != nil {
		return s.modelReviewUnavailable(ctx, access, source, content, result, latencyMS, err)
	}
	if !blocked {
		return nil
	}
	if err := s.recordModelReviewEvent(ctx, access, source, content, RiskEvent{
		EventType:       "model_blocked",
		Severity:        "critical",
		Action:          "request_blocked",
		MatchedRule:     category,
		ReviewModel:     result.Model,
		RiskScore:       float64Ptr(score),
		CategoryScores:  result.CategoryScores,
		ReviewLatencyMS: intPtr(latencyMS),
	}); err != nil {
		return errors.Join(
			Error{Kind: ErrUnavailable, Message: "AI 风控服务暂不可用"},
			fmt.Errorf("record model review block: %w", err),
		)
	}
	return Error{Kind: ErrContentBlocked, Message: "该内容未通过平台安全审核，请调整后重试"}
}

func (s *Service) modelReviewUnavailable(
	ctx context.Context,
	access StudentAccess,
	source string,
	content string,
	result ModelReviewResult,
	latencyMS int,
	cause error,
) error {
	publicErr := Error{Kind: ErrUnavailable, Message: "AI 风控服务暂不可用"}
	recordErr := s.recordModelReviewEvent(ctx, access, source, content, RiskEvent{
		EventType:       "model_review_error",
		Severity:        "warning",
		Action:          "review_failed_closed",
		MatchedRule:     "model_review_unavailable",
		ReviewModel:     result.Model,
		CategoryScores:  result.CategoryScores,
		ReviewLatencyMS: intPtr(latencyMS),
	})
	if recordErr != nil {
		return errors.Join(publicErr, fmt.Errorf("model review: %w", cause), fmt.Errorf("record model review error: %w", recordErr))
	}
	return errors.Join(publicErr, fmt.Errorf("model review: %w", cause))
}

func (s *Service) recordModelReviewEvent(
	ctx context.Context,
	access StudentAccess,
	source string,
	content string,
	event RiskEvent,
) error {
	eventID, err := s.newID()
	if err != nil {
		return fmt.Errorf("create model review event ID: %w", err)
	}
	now := s.now()
	studentID := access.StudentID
	digest := sha256.Sum256([]byte(content))
	event.ID = eventID
	event.StudentID = &studentID
	event.StudentUsername = access.Username
	event.Source = strings.TrimSpace(source)
	event.ContentExcerpt = contentExcerpt(content, 240)
	event.ContentHash = hex.EncodeToString(digest[:])
	event.ReviewModel = contentExcerpt(event.ReviewModel, 200)
	event.CategoryScores = cloneCategoryScores(event.CategoryScores)
	event.EventDate = s.usageDate(now)
	event.CreatedAt = now
	return s.repo.InsertRiskEvent(ctx, event)
}

func evaluateModelReview(result ModelReviewResult, thresholds map[string]float64) (bool, string, float64, error) {
	if strings.TrimSpace(result.Model) == "" {
		return false, "", 0, errors.New("model review returned an empty model")
	}
	if len(result.CategoryScores) == 0 {
		return false, "", 0, errors.New("model review returned no category scores")
	}
	for category, score := range result.CategoryScores {
		if strings.TrimSpace(category) == "" || math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
			return false, "", 0, errors.New("model review returned invalid category scores")
		}
	}
	blocked := false
	highestCategory := ""
	highestScore := 0.0
	for _, category := range modelReviewCategoryOrder {
		score, ok := result.CategoryScores[category]
		if !ok {
			return false, "", 0, fmt.Errorf("model review response missing category %q", category)
		}
		threshold, ok := thresholds[category]
		if !ok {
			return false, "", 0, fmt.Errorf("model review threshold missing category %q", category)
		}
		if highestCategory == "" || score > highestScore {
			highestCategory = category
			highestScore = score
		}
		if score >= threshold {
			blocked = true
		}
	}
	return blocked, highestCategory, highestScore, nil
}

func elapsedMilliseconds(startedAt time.Time) int {
	elapsed := time.Since(startedAt).Milliseconds()
	if elapsed <= 0 {
		return 0
	}
	if elapsed > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(elapsed)
}

func cloneCategoryScores(scores map[string]float64) map[string]float64 {
	if len(scores) == 0 {
		return map[string]float64{}
	}
	cloned := make(map[string]float64, len(scores))
	for category, score := range scores {
		cloned[category] = score
	}
	return cloned
}

func float64Ptr(value float64) *float64 { return &value }

func intPtr(value int) *int { return &value }

func (s *Service) recordContentBlock(ctx context.Context, access StudentAccess, source, content, matched string) error {
	eventID, err := s.newID()
	if err != nil {
		return fmt.Errorf("create content risk event ID: %w", err)
	}
	now := s.now()
	studentID := access.StudentID
	digest := sha256.Sum256([]byte(content))
	return s.repo.InsertRiskEvent(ctx, RiskEvent{
		ID:              eventID,
		StudentID:       &studentID,
		StudentUsername: access.Username,
		EventType:       "content_blocked",
		Severity:        "critical",
		Action:          "request_blocked",
		Source:          strings.TrimSpace(source),
		MatchedRule:     matched,
		ContentExcerpt:  contentExcerpt(content, 240),
		ContentHash:     hex.EncodeToString(digest[:]),
		EventDate:       s.usageDate(now),
		CreatedAt:       now,
	})
}

func matchBlockedKeyword(content string, keywords []string) string {
	content = strings.ToLower(strings.TrimSpace(content))
	if content == "" {
		return ""
	}
	for _, keyword := range keywords {
		if normalized := strings.ToLower(strings.TrimSpace(keyword)); normalized != "" && strings.Contains(content, normalized) {
			return keyword
		}
	}
	return ""
}

func contentExcerpt(content string, limit int) string {
	content = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, content))
	runes := []rune(content)
	if len(runes) <= limit {
		return content
	}
	return string(runes[:limit])
}
