// Package metadata contains the metadata of this plugin
package metadata

import "github.com/cloudnative-pg/cnpg-i/pkg/identity"

// PluginName is the name of the plugin
const PluginName = "cnpg-i-scale-to-zero.xata.io"

// Data is the metadata of this plugin
var Data = identity.GetPluginMetadataResponse{
	Name:          PluginName,
	Version:       "0.0.1",
	DisplayName:   "Plugin to scale down a CNPG PostgreSQL cluster to zero",
	ProjectUrl:    "https://github.com/xataio/cnpg-i-scale-to-zero",
	RepositoryUrl: "https://github.com/xataio/cnpg-i-scale-to-zero",
	License:       "Apache-2.0",
	LicenseUrl:    "https://github.com/xataio/cnpg-i-scale-to-zero/LICENSE",
	Maturity:      "alpha",
}
