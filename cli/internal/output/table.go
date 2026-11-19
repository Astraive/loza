package output

import (
	"fmt"
	"sort"
	"strings"
)

func PrintTable(headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
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
			if i < len(widths) {
				fmt.Printf("%-*s", widths[i], cell)
			}
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
		fmt.Printf("  %s  %s\n", Dim(fmt.Sprintf("%-*s", maxLen, k)), pairs[k])
	}
}
