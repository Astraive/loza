package output

import (
	"fmt"
	"sort"
	"strings"
)

func PrintTable(headers []string, rows [][]string) {
	// Size widths to the longest row as well as the header count so that cells
	// beyond the header count are not silently dropped during the width pass.
	n := len(headers)
	for _, row := range rows {
		if len(row) > n {
			n = len(row)
		}
	}
	widths := make([]int, n)
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	for i, h := range headers {
		if i > 0 {
			fmt.Print("  ")
		}
		fmt.Print(Bold(fmt.Sprintf("%-*s", widths[i], h)))
	}
	fmt.Println()
	for i := range headers {
		if i > 0 {
			fmt.Print("  ")
		}
		fmt.Printf("%s", strings.Repeat("-", widths[i]))
	}
	fmt.Println()
	for _, row := range rows {
		for i, cell := range row {
			if i > 0 {
				fmt.Print("  ")
			}
			fmt.Printf("%-*s", widths[i], cell)
		}
		fmt.Println()
	}
}

func PrintKeyValue(pairs map[string]string) {
	maxLen := 0
	keys := make([]string, 0, len(pairs))
	for k := range pairs {
		keys = append(keys, k)
		if len(k) > maxLen {
			maxLen = len(k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		// Pad the plain key first: Dim(k) contains ANSI escape bytes that
		// %-*s would otherwise count toward the width, breaking alignment.
		fmt.Printf("  %s  %s\n", Dim(fmt.Sprintf("%-*s", maxLen, k)), pairs[k])
	}
}
