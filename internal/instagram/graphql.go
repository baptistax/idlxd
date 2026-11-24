package instagram

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/baptistax/idl/internal/fault"
)

func decodeGraphQLResponse(body []byte, out any) error {
	root, err := readObject(body)
	if err != nil {
		return err
	}
	partial := envelopeError(root)
	if partial != nil && !fault.OnlyPartial(partial) {
		return partial
	}
	dec := json.NewDecoder(bytes.NewReader(normalizeGraphQLJSON(body)))
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		return schema("invalid field types")
	}
	return partial
}

func envelopeError(m object) error {
	// These are control fields on the response envelope, never caption text.
	for _, key := range []string{"checkpoint_url", "challenge_url", "challenge", "challenge_required", "checkpoint_required", "feedback_required", "feedback_title", "feedback_message", "account_warning", "warning_message", "spam", "is_spam"} {
		if v := m[key]; v != nil && v != false && v != "" {
			return fault.New(fault.Verification, "response", "platform verification or account warning")
		}
	}
	if m["require_login"] == true || m["authenticated"] == false {
		return fault.New(fault.Authentication, "response", "login required")
	}
	var failures []any
	if raw := m["errors"]; raw != nil {
		list, ok := raw.([]any)
		if !ok {
			return schema("invalid errors container")
		}
		failures = append(failures, list...)
	}
	legacy := false
	if v := m["error"]; v != nil && v != false && v != json.Number("0") {
		legacy = true
		failures = append(failures, v)
	}
	for _, key := range []string{"message", "error_title", "error_type", "errorDescription", "errorSummary"} {
		if s := str(m[key]); s != "" {
			failures = append(failures, s)
		}
	}
	incomplete := false
	for _, v := range failures {
		problem, _ := v.(object)
		if problem["requires_reauth"] == true {
			return fault.New(fault.Authentication, "response", "provider requires reauthentication")
		}
		raw, _ := json.Marshal(v)
		if kind := classifyApplicationError(raw); kind != fault.Upstream {
			return fault.New(kind, "response", "provider rejected request")
		}
		// A nullable place avatar can fail on an otherwise complete
		// timeline. Keep platform rejections and all media field errors.
		if m["data"] == nil || !timelineLocationAvatar(problem["path"]) {
			incomplete = true
		}
	}
	if legacy || m["status"] != nil && m["status"] != "ok" {
		return fault.New(fault.Upstream, "response", "application failure")
	}
	if incomplete {
		if m["data"] == nil {
			return fault.New(fault.Upstream, "response", "application failure without data")
		}
		return fault.New(fault.Partial, "response", "incomplete response")
	}
	return nil
}

func timelineLocationAvatar(raw any) bool {
	p, _ := raw.([]any)
	if len(p) != 6 {
		return false
	}
	_, index := p[2].(json.Number)
	return index && p[0] == postsConnection && p[1] == "edges" && p[3] == "node" && p[4] == "location" && p[5] == "profile_pic_url"
}

func classifyApplicationError(raw []byte) fault.Kind {
	var e struct {
		Code    int `json:"code"`
		Subcode int `json:"error_subcode"`
	}
	_ = json.Unmarshal(raw, &e)
	s := strings.ToLower(string(raw))
	switch {
	case e.Subcode == 459 || strings.Contains(s, "checkpoint") || strings.Contains(s, "challenge") || strings.Contains(s, "verification") || strings.Contains(s, "feedback_required") || strings.Contains(s, "automated behavior") || strings.Contains(s, "automated behaviour") || strings.Contains(s, "account warning") || strings.Contains(s, "suspended"):
		return fault.Verification
	case e.Code == 190 || e.Code == 102 || strings.Contains(s, "login_required") || strings.Contains(s, "session expired") || strings.Contains(s, "not logged in"):
		return fault.Authentication
	case e.Code == 4 || e.Code == 17 || e.Code == 32 || e.Code == 613 || e.Code == 80004 || strings.Contains(s, "rate limit") || strings.Contains(s, "please wait") || strings.Contains(s, "too many requests"):
		return fault.RateLimited
	case e.Code == 10 || e.Code == 200 || strings.Contains(s, "permission") || strings.Contains(s, "access denied") || strings.Contains(s, "not authorized") || strings.Contains(s, "restricted"):
		return fault.Permission
	default:
		return fault.Upstream
	}
}

func normalizeGraphQLJSON(body []byte) []byte {
	for {
		body = bytes.TrimSpace(body)
		switch {
		case bytes.HasPrefix(body, []byte("for (;;);")):
			body = body[9:]
		case bytes.HasPrefix(body, []byte("while(1);")):
			body = body[9:]
		case bytes.HasPrefix(body, []byte(")]}'")):
			body = body[4:]
		default:
			return body
		}
	}
}
