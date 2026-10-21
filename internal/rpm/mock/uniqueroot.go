// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package mock

import (
	"context"
	"crypto/rand"
	"strconv"
	"strings"
)

type uniqueRootKey struct{}

const (
	maxUniqueExtensionLength       = 24
	uniqueRandomLength             = 6
	maxRootActionLength            = 4
	maxRootActionWithoutComponents = 8
	maxRootComponentLength         = maxUniqueExtensionLength - maxRootActionLength - uniqueRandomLength - 2
)

type uniqueRootOptions struct {
	enabled    bool
	action     string
	components []string
}

// WithUniqueRoots configures newly created runners to use distinct mock roots.
func WithUniqueRoots(ctx context.Context, enabled bool, action string) context.Context {
	return context.WithValue(ctx, uniqueRootKey{}, uniqueRootOptions{
		enabled: enabled,
		action:  action,
	})
}

// WithRootComponents specializes the component label for newly created runners,
// without changing whether unique roots are enabled.
func WithRootComponents(ctx context.Context, components ...string) context.Context {
	options, _ := ctx.Value(uniqueRootKey{}).(uniqueRootOptions)

	options.components = append([]string(nil), components...)

	return context.WithValue(ctx, uniqueRootKey{}, options)
}

func newUniqueExtension(ctx context.Context) string {
	options, _ := ctx.Value(uniqueRootKey{}).(uniqueRootOptions)
	if !options.enabled {
		return ""
	}

	action := rootAction(options.action)

	label := action[:min(len(action), maxRootActionWithoutComponents)]
	if len(options.components) > 0 {
		action = action[:min(len(action), maxRootActionLength)]
		component := rootLabel(options.components[0])

		componentLabel := component[:min(len(component), maxRootComponentLength)]
		if len(options.components) > 1 {
			count := strconv.Itoa(len(options.components))

			batchLabel := component[:min(len(component), 1)] + "_" + count
			if len(batchLabel) <= maxRootComponentLength {
				componentLabel = batchLabel
			}
		}

		label = action + "-" + componentLabel
	}

	// Reserve six random base32 characters and a separator within the 24-byte limit.
	return label + "-" + strings.ToLower(rand.Text()[:uniqueRandomLength])
}

func rootAction(action string) string {
	action = rootLabel(action)
	switch action {
	case "build", "build-rpms":
		return "bld"
	case "render":
		return "rndr"
	case "query":
		return "qry"
	case "shell":
		return "shl"
	case "":
		return "mock"
	default:
		return action
	}
}

func rootLabel(value string) string {
	value = strings.ToLower(value)
	value = strings.Map(func(char rune) rune {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' || char == '-' {
			return char
		}

		return '-'
	}, value)

	return strings.Trim(value, "-_")
}
