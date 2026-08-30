package xidian

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

type loginPage struct {
	HiddenInputs   map[string]string
	ContinueInputs map[string]string
	PasswordSalt   string
	ErrorMessage   string
}

var (
	inputTagPattern     = regexp.MustCompile(`(?is)<input\b[^>]*>`)
	formContinuePattern = regexp.MustCompile(`(?is)<form\b[^>]*(?:id|name)=["']continue["'][^>]*>(.*?)</form>`)
	attrPattern         = regexp.MustCompile(`(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*("([^"]*)"|'([^']*)'|([^\s>]+))`)
	errorPattern        = regexp.MustCompile(`(?is)<[^>]*(?:id|class)=["'][^"']*showErrorTip[^"']*["'][^>]*>(.*?)</[^>]+>`)
	htmlTagPattern      = regexp.MustCompile(`(?is)<[^>]+>`)
)

func parseLoginPage(rawHTML string) loginPage {
	page := loginPage{HiddenInputs: map[string]string{}, ContinueInputs: map[string]string{}}
	for _, tag := range inputTagPattern.FindAllString(rawHTML, -1) {
		attrs := parseAttrs(tag)
		name := attrs["name"]
		if name == "" {
			name = attrs["id"]
		}
		if name == "" {
			continue
		}
		value := attrs["value"]
		if attrs["id"] == "pwdEncryptSalt" {
			page.PasswordSalt = value
		}
		if strings.EqualFold(attrs["type"], "hidden") {
			page.HiddenInputs[name] = value
		}
	}
	if match := formContinuePattern.FindStringSubmatch(rawHTML); len(match) == 2 {
		for _, tag := range inputTagPattern.FindAllString(match[1], -1) {
			attrs := parseAttrs(tag)
			name := attrs["name"]
			if name == "" {
				name = attrs["id"]
			}
			if name != "" {
				page.ContinueInputs[name] = attrs["value"]
			}
		}
	}
	if match := errorPattern.FindStringSubmatch(rawHTML); len(match) == 2 {
		page.ErrorMessage = strings.TrimSpace(html.UnescapeString(htmlTagPattern.ReplaceAllString(match[1], " ")))
	}
	return page
}

func parseAttrs(tag string) map[string]string {
	attrs := map[string]string{}
	for _, match := range attrPattern.FindAllStringSubmatch(tag, -1) {
		value := match[3]
		if value == "" {
			value = match[4]
		}
		if value == "" {
			value = match[5]
		}
		attrs[strings.ToLower(match[1])] = html.UnescapeString(value)
	}
	return attrs
}

func aesEncryptPassword(password string, salt string) (string, error) {
	if len(salt) != aes.BlockSize {
		return "", fmt.Errorf("invalid Xidian password salt length %d", len(salt))
	}
	prefix := "xidianscriptsxduxidianscriptsxduxidianscriptsxduxidianscriptsxdu"
	data := []byte(prefix + password)
	padding := aes.BlockSize - len(data)%aes.BlockSize
	data = append(data, bytes.Repeat([]byte{byte(padding)}, padding)...)
	block, err := aes.NewCipher([]byte(salt))
	if err != nil {
		return "", err
	}
	encrypted := make([]byte, len(data))
	// #nosec G407 -- Xidian's legacy login protocol mandates this fixed CBC IV.
	cipher.NewCBCEncrypter(block, []byte("xidianscriptsxdu")).CryptBlocks(encrypted, data)
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

func intFromAny(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return int(parsed)
		}
	case string:
		parsed, err := strconv.Atoi(typed)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func stringFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return value
}

func firstPresent(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok && value != nil {
			return value
		}
	}
	return nil
}
