package main

import (
	"fmt"
	"os/exec"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/watchtowerobservability/terraform-provider-watchtower/internal/ascode/ascodetest"
)

var factories = map[string]func() (tfprotov6.ProviderServer, error){
	"watchtower": providerserver.NewProtocol6WithError(New()),
}

// needTerraform skips the CLI-driven tests where no terraform binary is installed (the direct
// resource tests in ./resources cover the same CRUD without it).
func needTerraform(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform CLI not installed")
	}
}

func cfg(srv *ascodetest.Server, interval int) string {
	return fmt.Sprintf(`
provider "watchtower" {
  endpoint = %q
  api_key  = "test-key"
  project  = %q
}
resource "watchtower_monitor" "api" {
  name = "api-health"
  spec = jsonencode({
    env_id     = "env_1"
    kind       = "http"
    interval_s = %d
    config     = { url = "https://example.com/health" }
  })
}
`, srv.URL, ascodetest.Project, interval)
}

func TestTerraformMonitorCRUD(t *testing.T) {
	needTerraform(t)
	srv := ascodetest.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg(srv, 60), Check: resource.TestCheckResourceAttr("watchtower_monitor.api", "id", ascodetest.Project+"/api-health")},
			{Config: cfg(srv, 30), Check: func(*terraform.State) error {
				if got := srv.Body("Monitor", "api-health")["interval_s"]; got != float64(30) {
					return fmt.Errorf("interval_s = %v", got)
				}
				return nil
			}},
		},
		CheckDestroy: func(*terraform.State) error {
			if srv.Count("Monitor") != 0 {
				return fmt.Errorf("monitor left behind")
			}
			return nil
		},
	})
}

func TestTerraformImportState(t *testing.T) {
	needTerraform(t)
	srv := ascodetest.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg(srv, 60)},
			{ResourceName: "watchtower_monitor.api", ImportState: true, ImportStateId: ascodetest.Project + "/api-health", ImportStateVerify: false},
		},
	})
}
