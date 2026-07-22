// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package agentskill

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderInstructionRejectsInvalidSkillPointers(t *testing.T) {
	tests := []struct {
		name          string
		skills        []SkillPointer
		errorContains string
	}{
		{
			name:          "no skill pointers",
			errorContains: "must reference at least one skill",
		},
		{
			name: "unknown skill",
			skills: []SkillPointer{
				{Skill: "not-a-real-skill", Purpose: "for testing"},
			},
			errorContains: "references unknown skill `not-a-real-skill`",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := renderInstruction(Instruction{
				Name:   "test-instruction",
				Skills: test.skills,
			}, Params{})

			require.Error(t, err)
			assert.Contains(t, err.Error(), test.errorContains)
		})
	}
}
