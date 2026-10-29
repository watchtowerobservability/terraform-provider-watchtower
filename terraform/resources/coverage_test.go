package resources

import (
	"os"
	"strings"
	"testing"

	"github.com/watchtowerobservability/terraform-provider-watchtower/internal/ascode"
)

// families lists the resource families with CRUD routes and the as-code kind that covers each
// ("" = not covered, so it must appear in coverage_exempt.txt with a reason).
var families = map[string]string{
	"attribute-registry": "AttributeRegistry",
	"archive":            "ArchiveConfig",
	"fleet/configs":      "FleetConfig",
	"pipelines":          "Pipeline",
	"dashboards":         "Dashboard",
	"retention-rules":    "RetentionRule",
	"change-sources":     "ChangeSource",
	"shaping-rules":      "",
	"export-schedules":   "ExportSchedule",
	"ip-allowlists":      "IpAllowlist",
	"org-sso":            "OrgSso",
	"secret-stores":      "SecretStore",
}

func TestCoverageAuditEveryFamilyCoveredOrExempt(t *testing.T) {
	raw, err := os.ReadFile("coverage_exempt.txt")
	if err != nil {
		t.Fatal(err)
	}
	exempt := map[string]string{}
	for _, l := range strings.Split(string(raw), "\n") {
		if f, why, ok := strings.Cut(strings.TrimSpace(l), " # "); ok && strings.TrimSpace(why) != "" {
			exempt[f] = why
		}
	}
	for fam, kind := range families {
		if kind != "" {
			if ascode.ByKind(kind) == nil {
				t.Errorf("family %s: kind %s is not registered", fam, kind)
			}
			continue
		}
		if exempt[fam] == "" {
			t.Errorf("family %s has no resource and no reasoned entry in coverage_exempt.txt", fam)
		}
	}
}

func TestPhase6KindShapes(t *testing.T) {
	if k := ascode.ByKind("AttributeRegistry"); k.NameKey() != "key" || k.Update != "PATCH" {
		t.Fatalf("unexpected attribute registry kind %+v", k)
	}
	a := ascode.ByKind("ArchiveConfig")
	if !a.ByName || !a.NoBodyName || len(a.SecretFields) == 0 {
		t.Fatalf("archive config must be by-name, nameless in the body and secret-checked: %+v", a)
	}
}
