package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"
	"time"

	"mathstudy/backend/internal/domain/user"
	"mathstudy/backend/internal/platform/securerand"
)

const (
	jwtIssuer         = "math-study-platform"
	jwtAudience       = "msp-api"
	maxJWTEncodedSize = 4 * 1024
)

var errInvalidToken = errors.New("invalid token")

// TokenClaims contains the validated claims used by the current authentication boundary.
type TokenClaims struct {
	Subject     string
	Role        user.Role
	AuthVersion int64
	Type        string
	JTI         string
	Issued      time.Time
	Expires     time.Time
}

// TokenService creates and verifies HMAC JWTs used by the Go API.
type TokenService struct {
	secret     []byte
	algorithm  string
	accessTTL  time.Duration
	refreshTTL time.Duration
	now        func() time.Time
}

// NewTokenService builds a JWT service for HS256, HS384, or HS512.
func NewTokenService(secret, algorithm string, accessTTL, refreshTTL time.Duration) (TokenService, error) {
	algorithm = strings.ToUpper(strings.TrimSpace(algorithm))
	if _, err := hashForAlgorithm(algorithm); err != nil {
		return TokenService{}, err
	}
	if strings.TrimSpace(secret) == "" {
		return TokenService{}, errors.New("jwt secret key is empty")
	}
	if accessTTL <= 0 {
		return TokenService{}, errors.New("jwt access token ttl must be greater than zero")
	}
	if refreshTTL <= 0 {
		return TokenService{}, errors.New("jwt refresh token ttl must be greater than zero")
	}
	return TokenService{
		secret:     []byte(secret),
		algorithm:  algorithm,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
		now:        func() time.Time { return time.Now().UTC() },
	}, nil
}

// CreateAccessToken returns a signed access token bound to the account's current auth version.
func (s TokenService) CreateAccessToken(subject string, role user.Role, authVersion int64) (string, error) {
	if err := validateTokenIdentity(subject, authVersion); err != nil {
		return "", err
	}
	if _, err := user.ParseRole(string(role)); err != nil {
		return "", errors.New("access token role is invalid")
	}
	return s.createToken(subject, "access", s.accessTTL, map[string]any{
		"role":         string(role),
		"auth_version": authVersion,
	})
}

// CreateRefreshToken returns a signed refresh token bound to the account's current auth version.
func (s TokenService) CreateRefreshToken(subject string, authVersion int64) (string, error) {
	if err := validateTokenIdentity(subject, authVersion); err != nil {
		return "", err
	}
	return s.createToken(subject, "refresh", s.refreshTTL, map[string]any{"auth_version": authVersion})
}

// Decode verifies a token and returns its compatible claims.
func (s TokenService) Decode(token string) (TokenClaims, error) {
	if token == "" || len(token) > maxJWTEncodedSize {
		return TokenClaims{}, errInvalidToken
	}
	headerSegment, remainder, ok := strings.Cut(token, ".")
	if !ok || headerSegment == "" {
		return TokenClaims{}, errInvalidToken
	}
	claimsSegment, signatureSegment, ok := strings.Cut(remainder, ".")
	if !ok || claimsSegment == "" || signatureSegment == "" || strings.ContainsRune(signatureSegment, '.') {
		return TokenClaims{}, errInvalidToken
	}

	var header map[string]any
	if err := decodeSegment(headerSegment, &header); err != nil {
		return TokenClaims{}, errInvalidToken
	}
	alg, ok := header["alg"].(string)
	if !ok || alg != s.algorithm {
		return TokenClaims{}, errInvalidToken
	}

	expected, err := s.sign(headerSegment + "." + claimsSegment)
	if err != nil {
		return TokenClaims{}, err
	}
	if subtle.ConstantTimeCompare([]byte(signatureSegment), []byte(expected)) != 1 {
		return TokenClaims{}, errInvalidToken
	}

	var rawClaims map[string]any
	if err := decodeSegment(claimsSegment, &rawClaims); err != nil {
		return TokenClaims{}, errInvalidToken
	}
	if !claimMatches(rawClaims["iss"], jwtIssuer) || !audienceMatches(rawClaims["aud"], jwtAudience) {
		return TokenClaims{}, errInvalidToken
	}
	subject, _ := rawClaims["sub"].(string)
	tokenType, _ := rawClaims["type"].(string)
	jti, _ := rawClaims["jti"].(string)
	if subject == "" || jti == "" || (tokenType != "access" && tokenType != "refresh") {
		return TokenClaims{}, errInvalidToken
	}

	issued, ok := numericDate(rawClaims["iat"])
	if !ok {
		return TokenClaims{}, errInvalidToken
	}
	expires, ok := numericDate(rawClaims["exp"])
	if !ok || s.now().After(expires) {
		return TokenClaims{}, errInvalidToken
	}
	authVersion, ok := integerClaim(rawClaims["auth_version"])
	if !ok || authVersion < 1 {
		return TokenClaims{}, errInvalidToken
	}

	var role user.Role
	if roleValue, ok := rawClaims["role"].(string); ok && roleValue != "" {
		parsedRole, err := user.ParseRole(roleValue)
		if err != nil {
			return TokenClaims{}, errInvalidToken
		}
		role = parsedRole
	}
	if tokenType == "access" && role == "" {
		return TokenClaims{}, errInvalidToken
	}

	return TokenClaims{
		Subject:     subject,
		Role:        role,
		AuthVersion: authVersion,
		Type:        tokenType,
		JTI:         jti,
		Issued:      issued,
		Expires:     expires,
	}, nil
}

func (s TokenService) createToken(subject string, tokenType string, ttl time.Duration, extra map[string]any) (string, error) {
	now := s.now().UTC()
	jti, err := securerand.Hex(16)
	if err != nil {
		return "", err
	}
	claims := map[string]any{
		"exp":  now.Add(ttl).Unix(),
		"iat":  now.Unix(),
		"sub":  subject,
		"iss":  jwtIssuer,
		"aud":  jwtAudience,
		"jti":  jti,
		"type": tokenType,
	}
	for key, value := range extra {
		claims[key] = value
	}
	header := map[string]string{"alg": s.algorithm, "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(claimsJSON)
	signature, err := s.sign(unsigned)
	if err != nil {
		return "", err
	}
	return unsigned + "." + signature, nil
}

func (s TokenService) sign(unsigned string) (string, error) {
	hashFn, err := hashForAlgorithm(s.algorithm)
	if err != nil {
		return "", err
	}
	mac := hmac.New(hashFn, s.secret)
	_, _ = mac.Write([]byte(unsigned))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func hashForAlgorithm(algorithm string) (func() hash.Hash, error) {
	switch algorithm {
	case "HS256":
		return sha256.New, nil
	case "HS384":
		return sha512.New384, nil
	case "HS512":
		return sha512.New, nil
	default:
		return nil, fmt.Errorf("unsupported jwt algorithm %q", algorithm)
	}
}

func decodeSegment(segment string, target any) error {
	data, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errInvalidToken
		}
		return err
	}
	return nil
}

func claimMatches(value any, want string) bool {
	got, ok := value.(string)
	return ok && got == want
}

func audienceMatches(value any, want string) bool {
	if got, ok := value.(string); ok {
		return got == want
	}
	values, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range values {
		if got, ok := item.(string); ok && got == want {
			return true
		}
	}
	return false
}

func numericDate(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case json.Number:
		seconds, err := typed.Int64()
		if err != nil {
			return time.Time{}, false
		}
		return time.Unix(seconds, 0).UTC(), true
	case float64:
		return time.Unix(int64(typed), 0).UTC(), true
	case int64:
		return time.Unix(typed, 0).UTC(), true
	default:
		return time.Time{}, false
	}
}

func integerClaim(value any) (int64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	default:
		return 0, false
	}
}

func validateTokenIdentity(subject string, authVersion int64) error {
	if strings.TrimSpace(subject) == "" {
		return errors.New("jwt subject is empty")
	}
	if authVersion < 1 {
		return errors.New("jwt auth version must be positive")
	}
	return nil
}
