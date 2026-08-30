package mathsolver

import (
	"errors"
	"math/big"
	"strings"
)

func (c *Comparator) compareNumeric(student string, reference string, tolerance Tolerance) Result {
	studentValue, err := c.parseNumber(student)
	if err != nil {
		return numericParseFailure(err, "student_numeric", "学生答案无法解析为受支持的数值")
	}
	referenceValue, err := c.parseNumber(reference)
	if err != nil {
		return numericParseFailure(err, "reference_numeric", "标准答案无法解析为受支持的数值")
	}

	absoluteTolerance, err := c.parseTolerance(tolerance.Absolute)
	if err != nil {
		return toleranceFailure(err, "absolute_tolerance")
	}
	relativeTolerance, err := c.parseTolerance(tolerance.Relative)
	if err != nil {
		return toleranceFailure(err, "relative_tolerance")
	}

	difference := new(big.Rat).Sub(studentValue, referenceValue)
	difference.Abs(difference)
	if difference.Sign() == 0 {
		return decisiveResult(
			DecisionCorrect,
			MethodNumericExact,
			ReasonNumericExactMatch,
			"学生答案与标准答案表示同一精确数值",
			Evidence{Kind: "exact_rational", Summary: "两个答案转换为相同的有理数"},
		)
	}

	referenceMagnitude := new(big.Rat).Abs(new(big.Rat).Set(referenceValue))
	relativeThreshold := new(big.Rat).Mul(relativeTolerance, referenceMagnitude)
	threshold := maxRat(absoluteTolerance, relativeThreshold)
	method := MethodNumericExact
	if absoluteTolerance.Sign() > 0 || relativeTolerance.Sign() > 0 {
		method = MethodNumericTolerance
	}
	if difference.Cmp(threshold) <= 0 {
		return decisiveResult(
			DecisionCorrect,
			method,
			ReasonNumericWithinTolerance,
			"数值差异未超过允许的绝对或相对容差",
			Evidence{Kind: "numeric_tolerance", Summary: "精确有理数差值位于配置容差内"},
		)
	}
	return decisiveResult(
		DecisionIncorrect,
		method,
		ReasonNumericMismatch,
		"数值差异超过允许的绝对和相对容差",
		Evidence{Kind: "numeric_difference", Summary: "精确有理数差值超过配置容差"},
	)
}

func (c *Comparator) parseTolerance(raw string) (*big.Rat, error) {
	if strings.TrimSpace(raw) == "" {
		return new(big.Rat), nil
	}
	normalized, err := c.normalize(raw, AnswerKindNumeric)
	if err != nil {
		return nil, err
	}
	value, err := c.parseNumber(normalized)
	if err != nil {
		return nil, err
	}
	if value.Sign() < 0 {
		return nil, inputIssue{
			reasonCode:  ReasonToleranceInvalid,
			failureCode: FailureInvalidTolerance,
			message:     "数值容差不能为负数",
		}
	}
	return value, nil
}

func (c *Comparator) parseNumber(value string) (*big.Rat, error) {
	digits := 0
	for _, current := range value {
		if current >= '0' && current <= '9' {
			digits++
		}
	}
	if digits == 0 {
		return nil, numericSyntaxIssue()
	}
	if digits > c.limits.MaxNumericDigits {
		return nil, numericLimitIssue("数值有效数字超过确定性比较上限")
	}
	if strings.Count(value, "/") > 1 {
		return nil, numericSyntaxIssue()
	}
	if numerator, denominator, found := strings.Cut(value, "/"); found {
		if numerator == "" || denominator == "" {
			return nil, numericSyntaxIssue()
		}
		numeratorValue, err := c.parseScalar(numerator)
		if err != nil {
			return nil, err
		}
		denominatorValue, err := c.parseScalar(denominator)
		if err != nil {
			return nil, err
		}
		if denominatorValue.Sign() == 0 {
			return nil, inputIssue{
				reasonCode:  ReasonNumericParseFailed,
				failureCode: FailureNumericParse,
				message:     "分母不能为零",
			}
		}
		return new(big.Rat).Quo(numeratorValue, denominatorValue), nil
	}
	return c.parseScalar(value)
}

func (c *Comparator) parseScalar(value string) (*big.Rat, error) {
	if strings.Count(value, "*10^") > 1 {
		return nil, numericSyntaxIssue()
	}
	if coefficient, exponent, found := strings.Cut(value, "*10^"); found {
		if coefficient == "" || exponent == "" || strings.ContainsAny(coefficient, "eE") {
			return nil, numericSyntaxIssue()
		}
		coefficientValue, err := c.parseDecimal(coefficient)
		if err != nil {
			return nil, err
		}
		exponentValue, err := parseBoundedExponent(exponent, c.limits.MaxExponent)
		if err != nil {
			return nil, err
		}
		return scalePowerOfTen(coefficientValue, exponentValue), nil
	}
	return c.parseDecimal(value)
}

func (c *Comparator) parseDecimal(value string) (*big.Rat, error) {
	if value == "" {
		return nil, numericSyntaxIssue()
	}
	sign := 1
	switch value[0] {
	case '+':
		value = value[1:]
	case '-':
		sign = -1
		value = value[1:]
	}
	if value == "" {
		return nil, numericSyntaxIssue()
	}

	exponent := 0
	if index := strings.IndexAny(value, "eE"); index >= 0 {
		if strings.ContainsAny(value[index+1:], "eE") {
			return nil, numericSyntaxIssue()
		}
		parsedExponent, err := parseBoundedExponent(value[index+1:], c.limits.MaxExponent)
		if err != nil {
			return nil, err
		}
		exponent = parsedExponent
		value = value[:index]
	}

	if strings.Count(value, ".") > 1 {
		return nil, numericSyntaxIssue()
	}
	integerPart, fractionalPart, hasDecimal := strings.Cut(value, ".")
	if !hasDecimal {
		integerPart = value
	}
	if integerPart == "" && fractionalPart == "" {
		return nil, numericSyntaxIssue()
	}
	if integerPart == "" {
		integerPart = "0"
	}
	if !asciiDigits(integerPart) || (fractionalPart != "" && !asciiDigits(fractionalPart)) {
		return nil, numericSyntaxIssue()
	}

	digits := strings.TrimLeft(integerPart+fractionalPart, "0")
	if digits == "" {
		return new(big.Rat), nil
	}
	numerator := new(big.Int)
	if _, ok := numerator.SetString(digits, 10); !ok {
		return nil, numericSyntaxIssue()
	}
	if sign < 0 {
		numerator.Neg(numerator)
	}
	denominator := powerOfTen(len(fractionalPart))
	valueAsRat := new(big.Rat).SetFrac(numerator, denominator)
	return scalePowerOfTen(valueAsRat, exponent), nil
}

func parseBoundedExponent(value string, maximum int) (int, error) {
	if value == "" {
		return 0, numericSyntaxIssue()
	}
	sign := 1
	switch value[0] {
	case '+':
		value = value[1:]
	case '-':
		sign = -1
		value = value[1:]
	}
	if value == "" || !asciiDigits(value) {
		return 0, numericSyntaxIssue()
	}
	parsed := 0
	for _, current := range value {
		digit := int(current - '0')
		if parsed > maximum/10 || (parsed == maximum/10 && digit > maximum%10) {
			return 0, numericLimitIssue("科学计数法指数超过确定性比较上限")
		}
		parsed = parsed*10 + digit
	}
	return sign * parsed, nil
}

func scalePowerOfTen(value *big.Rat, exponent int) *big.Rat {
	if exponent == 0 || value.Sign() == 0 {
		return new(big.Rat).Set(value)
	}
	scale := new(big.Rat).SetInt(powerOfTen(absInt(exponent)))
	if exponent > 0 {
		return new(big.Rat).Mul(value, scale)
	}
	return new(big.Rat).Quo(value, scale)
}

func powerOfTen(exponent int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exponent)), nil)
}

func asciiDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, current := range value {
		if current < '0' || current > '9' {
			return false
		}
	}
	return true
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func maxRat(left *big.Rat, right *big.Rat) *big.Rat {
	if left.Cmp(right) >= 0 {
		return new(big.Rat).Set(left)
	}
	return new(big.Rat).Set(right)
}

func numericSyntaxIssue() inputIssue {
	return inputIssue{
		reasonCode:  ReasonNumericParseFailed,
		failureCode: FailureNumericParse,
		message:     "数值格式不受支持",
	}
}

func numericLimitIssue(message string) inputIssue {
	return inputIssue{
		reasonCode:  ReasonInputLimitExceeded,
		failureCode: FailureInputLimitExceeded,
		message:     message,
	}
}

func numericParseFailure(err error, stage string, reason string) Result {
	var issue inputIssue
	if errors.As(err, &issue) && issue.failureCode == FailureInputLimitExceeded {
		return failureResult(issue.reasonCode, issue.message, issue.failureCode, stage, false)
	}
	return failureResult(ReasonNumericParseFailed, reason, FailureNumericParse, stage, false)
}

func toleranceFailure(err error, stage string) Result {
	var issue inputIssue
	if errors.As(err, &issue) && issue.failureCode == FailureInputLimitExceeded {
		return failureResult(issue.reasonCode, issue.message, issue.failureCode, stage, false)
	}
	return failureResult(
		ReasonToleranceInvalid,
		"数值容差必须是非负且受支持的数值",
		FailureInvalidTolerance,
		stage,
		false,
	)
}
