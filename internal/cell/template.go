package cell

import "strings"

func renderTemplate(input string, variables map[string]string) string {
	output := input

	for key, value := range variables {
		output = strings.ReplaceAll(output, "{{"+key+"}}", value)
	}

	return output
}
