package manager

import (
	"encoding/json"
	"fmt"

	"github.com/viant/mcp-protocol/schema"
	"testing"
)

var decodedCatalogBenchmark *schema.ListToolsResult

func BenchmarkCachedToolCatalogDecode(b *testing.B) {
	for _, count := range []int{100, 500} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			base := representativeBenchTools(b, "synthetic")
			tools := make([]schema.Tool, count)
			for i := range tools {
				tools[i] = base[i%len(base)]
				tools[i].Name = fmt.Sprintf("lookup_%d", i)
			}
			wire, err := json.Marshal(&schema.ListToolsResult{Tools: tools})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(wire)))
			for b.Loop() {
				decodedCatalogBenchmark, err = decodeToolCatalogPage(wire)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Compare the existing metadata copier with wire decoding before changing cache storage.
func BenchmarkCachedToolCatalogCopy(b *testing.B) {
	for _, count := range []int{100, 500} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			base := representativeBenchTools(b, "synthetic")
			tools := make([]schema.Tool, count)
			for i := range tools {
				tools[i] = base[i%len(base)]
				tools[i].Name = fmt.Sprintf("lookup_%d", i)
			}
			wire, err := json.Marshal(&schema.ListToolsResult{Tools: tools})
			if err != nil {
				b.Fatal(err)
			}
			source, err := prepareToolCatalogContent(wire)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				result, err := source.Copy()
				if err != nil {
					b.Fatal(err)
				}
				decodedCatalogBenchmark = result
			}
		})
	}
}
