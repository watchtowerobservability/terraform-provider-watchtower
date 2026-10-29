// Command terraform-provider-watchtower is the Watchtower Terraform provider.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

func main() {
	debug := flag.Bool("debug", false, "serve with delve support")
	flag.Parse()
	err := providerserver.Serve(context.Background(), New, providerserver.ServeOpts{
		Address: "registry.terraform.io/watchtower/watchtower",
		Debug:   *debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
