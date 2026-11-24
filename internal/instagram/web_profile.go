package instagram

import (
	"bytes"
	"encoding/json"
	"regexp"

	"github.com/baptistax/idl/internal/fault"
	"github.com/baptistax/idl/internal/models"
	"github.com/baptistax/idl/internal/transport"
)

// The signed-in viewer and the requested profile are separate server records.
type profilePage struct {
	profile              Profile
	viewer               string
	viewerName           string
	routeID              string
	dtsg, lsd            string
	privateAllowed       bool
	timeline, tray       object
	timelineErr, trayErr error
}

var scriptElement = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script\s*>`)
var jsonScriptType = regexp.MustCompile(`(?i)\s+type\s*=\s*(?:"application/json"|'application/json'|application/json(?:\s|$))`)

// ErrTargetProfileMissing is returned only after every response fragment has
// been checked and the server has identified the signed-in viewer.
var ErrTargetProfileMissing = schema("signed-in viewer verified but target profile missing")

func parseProfilePage(body []byte, username string) (profilePage, error) {
	var page profilePage
	if len(body) > transport.MaxBody {
		return page, schema("profile response too large")
	}
	if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("<")) {
		if _, err := webResponse(body); err != nil {
			return page, err
		}
		return page, schema("expected profile page")
	}
	receive := func(result object) error { return page.receive(result, username) }
	for _, m := range scriptElement.FindAllSubmatch(body, -1) {
		if !jsonScriptType.Match(m[1]) {
			continue
		}
		root, err := readObject(m[2])
		if err != nil {
			return profilePage{}, err
		}
		if err := envelopeError(root); err != nil {
			return profilePage{}, err
		}
		if err := page.readModules(root, username); err != nil {
			return profilePage{}, err
		}
		if err := relayResults(root, false, receive); err != nil {
			return profilePage{}, err
		}
	}
	if page.viewer == "" {
		return profilePage{}, fault.New(fault.Authentication, "profile", "no signed-in viewer in response")
	}
	if page.profile.UserID == "" && page.routeID != "" {
		return page, ErrTargetProfileMissing
	}
	if err := page.verifyProfile(); err != nil {
		return profilePage{}, err
	}
	return page, nil
}

func (page *profilePage) receive(result object, username string) error {
	partial := envelopeError(result)
	if partial != nil && !fault.OnlyPartial(partial) {
		return partial
	}
	data, _ := result["data"].(object)
	if err := envelopeError(data); err != nil {
		return err
	}
	if raw, exists := data["viewer"]; exists {
		if partial != nil {
			return partial
		}
		v, ok := raw.(object)
		if raw == nil || ok && v["user"] == nil {
			return fault.New(fault.Authentication, "profile", "Relay returned no signed-in viewer")
		}
		u, _ := v["user"].(object)
		id, err := accountID(u)
		if err != nil {
			return schema("invalid Relay viewer identity")
		}
		if err := page.recordViewer(id, u["username"]); err != nil {
			return err
		}
	}
	if u, ok := data["user"].(object); ok && u["username"] != nil {
		if _, err := accountID(u); err != nil {
			return err
		}
		if partial != nil {
			return partial
		}
		raw, err := json.Marshal(u)
		if err != nil {
			return schema("invalid profile object")
		}
		p, err := ParseProfile(raw, username)
		if err != nil {
			return err
		}
		if page.profile.UserID != "" && page.profile.UserID != p.UserID {
			return mismatch("conflicting target profiles")
		}
		page.profile = p
		friend, _ := u["friendship_status"].(object)
		page.privateAllowed = friend["following"] == true
	}
	return findFields(data, []string{postsConnection, "edge_owner_to_timeline_media", "highlights"}, func(key string, v any, parent object) error {
		container := object{key: v, "id": parent["id"], "pk": parent["pk"], "username": parent["username"]}
		if key == "highlights" {
			if page.tray != nil {
				return schema("ambiguous highlights tray")
			}
			page.tray, page.trayErr = container, partial
		} else {
			if page.timeline != nil {
				return schema("ambiguous profile timeline")
			}
			page.timeline, page.timelineErr = container, partial
		}
		return nil
	})
}

func (page *profilePage) verifyProfile() error {
	if page.profile.UserID == "" {
		return ErrTargetProfileMissing
	}
	if page.routeID != "" && page.routeID != page.profile.UserID {
		return mismatch("profile and route identifiers disagree")
	}
	if page.viewerName != "" && (page.viewer == page.profile.UserID) != (page.viewerName == page.profile.Username) {
		return mismatch("viewer and target account records disagree")
	}
	if page.profile.IsPrivate && page.viewer != page.profile.UserID && !page.privateAllowed {
		return mismatch("private profile access unconfirmed")
	}
	return nil
}

func (p *profilePage) recordViewer(id string, rawName any) error {
	if p.viewer != "" && p.viewer != id {
		return mismatch("conflicting session identities")
	}
	if rawName != nil {
		name, err := models.NormalizeUsername(str(rawName))
		if err != nil {
			return schema("invalid viewer account name")
		}
		if p.viewerName != "" && p.viewerName != name {
			return mismatch("conflicting viewer account names")
		}
		p.viewerName = name
	}
	p.viewer = id
	return nil
}

// Only read definitions delivered by the server's module loader. Media,
// profile records and arbitrary arrays may also contain names and IDs.
func (p *profilePage) readModules(root object, username string) error {
	calls, _ := root["require"].([]any)
	for _, call := range calls {
		tuple, _ := call.([]any)
		if len(tuple) < 4 || (tuple[0] != "ScheduledServerJS" && tuple[0] != "ScheduledServerJSWithCSS") || tuple[1] != "handle" {
			continue
		}
		args, _ := tuple[3].([]any)
		for _, arg := range args {
			packet, _ := arg.(object)
			box, _ := packet["__bbox"].(object)
			if err := p.readProfileRoute(box, username); err != nil {
				return err
			}
			defs, _ := box["define"].([]any)
			for _, entry := range defs {
				def, _ := entry.([]any)
				if len(def) < 3 {
					if len(def) > 0 && def[0] == "PolarisViewer" {
						return schema("incomplete viewer module")
					}
					continue
				}
				value, _ := def[2].(object)
				switch def[0] {
				case "DTSGInitialData":
					p.dtsg = str(value["token"])
				case "LSD":
					p.lsd = str(value["token"])
				case "PolarisViewer":
					if err := p.readViewerIdentity(value); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// A route identifies which account to query when Relay hasn't embedded its
// profile. Only the initial profile view in the router bootstrap is relevant.
func (p *profilePage) readProfileRoute(box object, username string) error {
	calls, _ := box["require"].([]any)
	for _, raw := range calls {
		call, _ := raw.([]any)
		if len(call) < 4 || call[0] != "CometPlatformRootClient" || call[1] != "initialize" {
			continue
		}
		args, _ := call[3].([]any)
		for _, arg := range args {
			payload, _ := arg.(object)
			initial, _ := payload["initialRouteInfo"].(object)
			route, _ := initial["route"].(object)
			view, _ := route["rootView"].(object)
			resource, _ := view["resource"].(object)
			if resource["__dr"] != "PolarisProfilePostsTabRoot.react" {
				continue
			}
			params, _ := route["params"].(object)
			name, err := models.NormalizeUsername(str(params["username"]))
			props, _ := view["props"].(object)
			id := numberText(props["id"])
			if err != nil || name != username || !models.IsNumericID(id) || p.routeID != "" && p.routeID != id {
				return mismatch("invalid target profile route")
			}
			p.routeID = id
		}
	}
	return nil
}

func (p *profilePage) readViewerIdentity(value object) error {
	if err := envelopeError(value); err != nil {
		return err
	}
	outerID, hasID := value["id"]
	raw, hasData := value["data"]
	if hasID && hasData && raw == nil && (outerID == nil || numberText(outerID) == "0") {
		return fault.New(fault.Authentication, "profile", "server viewer module reports a signed-out session")
	}
	user, _ := raw.(object)
	if err := envelopeError(user); err != nil {
		return err
	}
	id, err := accountID(user)
	if err != nil || !models.IsNumericID(numberText(outerID)) {
		return schema("incomplete viewer module identity")
	}
	if numberText(outerID) != id {
		return mismatch("viewer module identifiers disagree")
	}
	if user["username"] == nil {
		return schema("missing viewer module account name")
	}
	return p.recordViewer(id, user["username"])
}

func relayResults(v any, inRelay bool, receive func(object) error) error {
	switch n := v.(type) {
	case object:
		if inRelay {
			if res, ok := n["result"].(object); ok {
				return receive(res)
			}
		}
		for _, key := range []string{"require", "__bbox"} {
			if err := relayResults(n[key], inRelay, receive); err != nil {
				return err
			}
		}
	case []any:
		if len(n) > 0 {
			switch str(n[0]) {
			case "RelayPrefetchedStreamCache":
				if len(n) < 4 || n[1] != "next" {
					return nil
				}
				return relayResults(n[3], true, receive)
			case "PolarisChallengeRoot", "PolarisCheckpointRoot", "PolarisAutomatedBehaviorWarning", "PolarisFeedbackRequiredPage":
				return fault.New(fault.Verification, "profile", "account verification or warning")
			}
		}
		for _, el := range n {
			if err := relayResults(el, inRelay, receive); err != nil {
				return err
			}
		}
	}
	return nil
}
