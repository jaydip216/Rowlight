package store

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func BenchmarkEncodeLargeResult(b *testing.B) {
	const (
		rowCount  = 1000
		cellSize  = 8 << 10
		batchSize = 128
	)
	payload := strings.Repeat("x", cellSize)
	b.SetBytes(rowCount * cellSize)
	b.ReportAllocs()
	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		encoder := json.NewEncoder(io.Discard)
		batch := make([][]any, 0, batchSize)
		for rowIndex := 0; rowIndex < rowCount; rowIndex++ {
			value, _, _ := transportValueForType(payload, "TEXT", 1<<20)
			batch = append(batch, []any{value})
			if len(batch) == cap(batch) {
				if err := encoder.Encode(map[string]any{"type": "rows", "rows": batch}); err != nil {
					b.Fatal(err)
				}
				batch = make([][]any, 0, batchSize)
			}
		}
		if len(batch) > 0 {
			if err := encoder.Encode(map[string]any{"type": "rows", "rows": batch}); err != nil {
				b.Fatal(err)
			}
		}
	}
}
