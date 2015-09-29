// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"sort"
	"strings"

	"github.com/juju/cmd"
	"launchpad.net/gnuflag"

	"github.com/juju/juju/apiserver/params"
)

const ListCommandDoc = `
List information about image metadata stored in Juju environment.
This list can be filtered using various filters as described below.

If no filters are supplied, all stored image metadata will be listed.

options:
-e, --environment (= "")
   juju environment to operate in
-o, --output (= "")
   specify an output file
--format (= tabular)
   specify output format (json|tabular|yaml)
--stream
   image stream
--region
   cloud region
--series
   comma separated list of series
--arch
   comma separated list of architectures
--virtType
   virtualisation type, e.g. pv
--storageType
   root storage type, e.g. ebs   
`

// ListImagesCommand returns stored image metadata.
type ListImagesCommand struct {
	CloudImageMetadataCommandBase

	out cmd.Output

	Stream         string
	Region         string
	Series         []string
	Arches         []string
	VirtType       string
	RooStorageType string
}

// Init implements Command.Init.
func (c *ListImagesCommand) Init(args []string) (err error) {
	if len(c.Series) > 0 {
		result := []string{}
		for _, one := range c.Series {
			result = append(result, strings.Split(one, ",")...)
		}
		c.Series = result
	}
	if len(c.Arches) > 0 {
		result := []string{}
		for _, one := range c.Arches {
			result = append(result, strings.Split(one, ",")...)
		}
		c.Arches = result
	}
	return nil
}

// Info implements Command.Info.
func (c *ListImagesCommand) Info() *cmd.Info {
	return &cmd.Info{
		Name:    "list-images",
		Purpose: "lists image metadata for environment",
		Doc:     ListCommandDoc,
	}
}

// SetFlags implements Command.SetFlags.
func (c *ListImagesCommand) SetFlags(f *gnuflag.FlagSet) {
	c.CloudImageMetadataCommandBase.SetFlags(f)

	f.StringVar(&c.Stream, "stream", "", "image metadata stream")
	f.StringVar(&c.Region, "region", "", "image metadata cloud region")

	f.Var(cmd.NewAppendStringsValue(&c.Series), "series", "only show cloud image metadata for these series")
	f.Var(cmd.NewAppendStringsValue(&c.Arches), "arch", "only show cloud image metadata for these architectures")

	f.StringVar(&c.VirtType, "virtType", "", "image metadata virtualisation type")
	f.StringVar(&c.RooStorageType, "storageType", "", "image metadata root storage type")

	c.out.AddFlags(f, "tabular", map[string]cmd.Formatter{
		"yaml":    cmd.FormatYaml,
		"json":    cmd.FormatJson,
		"tabular": formatMetadataListTabular,
	})
}

// Run implements Command.Run.
func (c *ListImagesCommand) Run(ctx *cmd.Context) (err error) {
	api, err := getImageMetadataListAPI(c)
	if err != nil {
		return err
	}
	defer api.Close()

	found, err := api.List(c.Stream, c.Region, c.Series, c.Arches, c.VirtType, c.RooStorageType)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		return nil
	}

	info := convertDetailsToInfo(found)
	var output interface{}
	switch c.out.Name() {
	case "yaml", "json":
		output = groupMetadata(info)
	default:
		{
			sort.Sort(metadataInfos(info))
			output = info
		}
	}
	return c.out.Write(ctx, output)
}

var getImageMetadataListAPI = (*ListImagesCommand).getImageMetadataListAPI

// MetadataListAPI defines the API methods that list image metadata command uses.
type MetadataListAPI interface {
	Close() error
	List(stream, region string, series, arches []string, virtType, rootStorageType string) ([]params.CloudImageMetadata, error)
}

func (c *ListImagesCommand) getImageMetadataListAPI() (MetadataListAPI, error) {
	return c.NewImageMetadataAPI()
}

// convertDetailsToInfo converts cloud image metadata received from api to
// structure native to CLI and sort it.
func convertDetailsToInfo(details []params.CloudImageMetadata) []MetadataInfo {
	if len(details) == 0 {
		return nil
	}

	info := make([]MetadataInfo, len(details))
	for i, one := range details {
		info[i] = MetadataInfo{
			Source:          one.Source,
			Series:          one.Series,
			Arch:            one.Arch,
			Region:          one.Region,
			ImageId:         one.ImageId,
			Stream:          one.Stream,
			VirtType:        one.VirtType,
			RootStorageType: one.RootStorageType,
		}
	}
	return info
}

// metadataInfos is a convenience type enabling to sort
// a collection of MetadataInfo
type metadataInfos []MetadataInfo

// Implements sort.Interface
func (m metadataInfos) Len() int {
	return len(m)
}

// Implements sort.Interface and sort image metadata
// by source, series, arch and region.
// All properties are sorted in alphabetical order
// except for series which is reversed -
// latest series are at the beginning of the collection.
func (m metadataInfos) Less(i, j int) bool {
	if m[i].Source != m[j].Source {
		// alphabetical order
		return m[i].Source < m[j].Source
	}
	if m[i].Series != m[j].Series {
		// reverse order
		return m[i].Series > m[j].Series
	}
	if m[i].Arch != m[j].Arch {
		// alphabetical order
		return m[i].Arch < m[j].Arch
	}
	// alphabetical order
	return m[i].Region < m[j].Region
}

// Implements sort.Interface
func (m metadataInfos) Swap(i, j int) {
	m[i], m[j] = m[j], m[i]
}

type minMetadataInfo struct {
	ImageId         string `yaml:"image_id" json:"image_id"`
	Stream          string `yaml:"stream" json:"stream"`
	VirtType        string `yaml:"virt_type" json:"virt_type"`
	RootStorageType string `yaml:"storage_type" json:"storage_type"`
}

// groupMetadata constructs map representation of metadata
// grouping individual items by source, series, arch and region
// to be served to Yaml and JSON output for readability.
func groupMetadata(metadata []MetadataInfo) map[string]map[string]map[string]map[string][]minMetadataInfo {
	result := map[string]map[string]map[string]map[string][]minMetadataInfo{}

	for _, m := range metadata {
		sourceMap, ok := result[m.Source]
		if !ok {
			sourceMap = map[string]map[string]map[string][]minMetadataInfo{}
			result[m.Source] = sourceMap
		}

		seriesMap, ok := sourceMap[m.Series]
		if !ok {
			seriesMap = map[string]map[string][]minMetadataInfo{}
			sourceMap[m.Series] = seriesMap
		}

		archMap, ok := seriesMap[m.Arch]
		if !ok {
			archMap = map[string][]minMetadataInfo{}
			seriesMap[m.Arch] = archMap
		}

		if len(archMap[m.Region]) == 0 {
			archMap[m.Region] = []minMetadataInfo{}
		}
		archMap[m.Region] = append(archMap[m.Region], minMetadataInfo{m.ImageId, m.Stream, m.VirtType, m.RootStorageType})
	}

	return result
}
