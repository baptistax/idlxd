package instagram

import (
	"strings"
	"testing"

	"github.com/baptistax/idl/internal/fault"
)

func TestServerViewerIdentity(t *testing.T) {
	const viewer = `{"id":"9007199254740993","data":{"id":9007199254740993,"username":"viewer"}}`
	const other = `{"id":"234","username":"target"}`
	const self = `{"id":9007199254740993,"username":"viewer"}`
	const prefix = `<script type="application/json">{"require":[["ScheduledServerJS","handle",null,[{"__bbox":{"define":[["PolarisViewer",[],`
	const suffix = `,1]]}}]]]}</script>`
	module := func(v string) string { return prefix + v + suffix }
	targetOnly := func(target string) string {
		return strings.Replace(testPage(target, `null`, "", ""), `,"viewer":{"user":null}`, "", 1)
	}
	for _, tc := range []struct {
		name, html, target string
		want               fault.Kind
		own                bool
	}{
		{"separate viewer and target", module(viewer) + targetOnly(other), "target", "", false},
		{"self profile", targetOnly(self) + module(viewer), "viewer", "", true},
		{"agrees with Relay", testPage(other, `{"pk":"9007199254740993"}`, "", "") + module(viewer), "target", "", false},
		{"conflicting Relay", module(viewer) + testPage(other, `{"id":"345"}`, "", ""), "target", fault.Permission, false},
		{"conflicting viewer names", module(viewer) + module(strings.Replace(viewer, "viewer", "different", 1)) + targetOnly(other), "target", fault.Permission, false},
		{"conflicting target name", module(viewer) + targetOnly(strings.Replace(self, "viewer", "target", 1)), "target", fault.Permission, false},
		{"conflicting module IDs", module(strings.Replace(viewer, `"id":"9007199254740993"`, `"id":"4"`, 1)) + targetOnly(other), "target", fault.Permission, false},
		{"signed-out module overrides Relay", testPage(other, `{"pk":"9007199254740993"}`, "", "") + module(`{"id":"0","data":null}`), "target", fault.Authentication, false},
		{"signed-out Relay overrides module", testPage(other, "null", "", "") + module(viewer), "target", fault.Authentication, false},
		{"warning overrides identity", module(viewer) + targetOnly(other) + `<script type="application/json">{"require":[["PolarisChallengeRoot",null,[]]]}</script>`, "target", fault.Verification, false},
		{"incomplete module", module(`{"id":9,"data":{"id":9}}`) + targetOnly(other), "target", fault.Schema, false},
		{"wrong module context", strings.Replace(module(viewer), "ScheduledServerJS", "other", 1) + targetOnly(other), "target", fault.Authentication, false},
		{"viewer without target", module(viewer), "viewer", fault.Schema, false},
		{"target without viewer", targetOnly(other), "target", fault.Authentication, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, err := parseProfilePage([]byte(tc.html), tc.target)
			if tc.want != "" {
				if !fault.Is(err, tc.want) || page.profile.UserID != "" || page.viewer != "" {
					t.Fatalf("expected closed gate: %v", err)
				}
				return
			}
			if err != nil || page.viewer != "9007199254740993" || page.profile.Username != tc.target || (page.viewer == page.profile.UserID) != tc.own {
				t.Fatalf("incorrect identity result: %v", err)
			}
		})
	}
}
