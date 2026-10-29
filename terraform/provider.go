package main

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/watchtowerobservability/terraform-provider-watchtower/terraform/resources"
)

type watchtower struct{}

// New is the provider factory.
func New() provider.Provider { return &watchtower{} }

type providerModel struct {
	Endpoint types.String `tfsdk:"endpoint"`
	APIKey   types.String `tfsdk:"api_key"`
	Project  types.String `tfsdk:"project"`
	Org      types.String `tfsdk:"org"`
}

func (*watchtower) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "watchtower"
}

func (*watchtower) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"endpoint": schema.StringAttribute{Optional: true, Description: "API base URL; default $WT_API."},
		"api_key":  schema.StringAttribute{Optional: true, Sensitive: true, Description: "API key; default $WT_API_KEY."},
		"project":  schema.StringAttribute{Optional: true, Description: "Default project id for resources that set none; default $WT_PROJECT."},
		"org":      schema.StringAttribute{Optional: true, Description: "Default org id for org-scoped resources (ip_allowlist, org_sso, secret_store); default $WT_ORG."},
	}}
}

func orEnv(v types.String, env string) string {
	if v.ValueString() != "" {
		return v.ValueString()
	}
	return os.Getenv(env)
}

func (*watchtower) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var m providerModel
	if resp.Diagnostics.Append(req.Config.Get(ctx, &m)...); resp.Diagnostics.HasError() {
		return
	}
	cfg := &resources.Config{API: orEnv(m.Endpoint, "WT_API"), Key: orEnv(m.APIKey, "WT_API_KEY"), Project: orEnv(m.Project, "WT_PROJECT"), Org: orEnv(m.Org, "WT_ORG")}
	if cfg.API == "" || cfg.Key == "" {
		resp.Diagnostics.AddError("missing credentials", "set endpoint and api_key (or $WT_API and $WT_API_KEY)")
		return
	}
	resp.ResourceData = cfg
}

func (*watchtower) Resources(context.Context) []func() resource.Resource { return resources.All() }

func (*watchtower) DataSources(context.Context) []func() datasource.DataSource { return nil }
