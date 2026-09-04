// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package repocompare

import (
	"fmt"
	"sort"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/rpm"
)

// PackageStatus summarizes why a package name appears in the comparison.
type PackageStatus string

const (
	// PackageStatusMissingFromRight indicates that the left contains an identity absent from the right.
	PackageStatusMissingFromRight PackageStatus = "missing-from-right"
	// PackageStatusAddedInRight indicates that the right contains a package name absent from the left.
	PackageStatusAddedInRight PackageStatus = "added-in-right"
	// PackageStatusArchitecturesDiffer indicates that matching NEVRs have different architecture sets.
	PackageStatusArchitecturesDiffer PackageStatus = "architectures-differ"
)

// PackageReport summarizes both inventories for one differing package name.
type PackageReport struct {
	Name       string `json:"name"       table:"Name"`
	Summary    string `json:"summary"    table:"Summary"`
	LeftNEVRs  string `json:"leftNevrs"  table:"Left NEVRs"`
	RightNEVRs string `json:"rightNevrs" table:"Right NEVRs"`
}

// Compare returns one package-centric report for each package name with inventory differences.
func Compare(left, right []Package) ([]PackageReport, error) {
	leftByName := groupByName(left)
	rightByName := groupByName(right)
	names := unionNames(leftByName, rightByName)
	reports := make([]PackageReport, 0, len(names))

	for _, name := range names {
		leftPackages := leftByName[name]
		rightPackages := rightByName[name]

		statuses := packageStatuses(leftPackages, rightPackages)
		if len(statuses) == 0 {
			continue
		}

		leftNEVRs, err := sortedNEVRs(leftPackages)
		if err != nil {
			return nil, fmt.Errorf("sorting left inventory for package %#q:\n%w", name, err)
		}

		rightNEVRs, err := sortedNEVRs(rightPackages)
		if err != nil {
			return nil, fmt.Errorf("sorting right inventory for package %#q:\n%w", name, err)
		}

		reports = append(reports, PackageReport{
			Name:       name,
			Summary:    joinStatuses(statuses),
			LeftNEVRs:  strings.Join(leftNEVRs, ", "),
			RightNEVRs: strings.Join(rightNEVRs, ", "),
		})
	}

	return reports, nil
}

func packageStatuses(left, right []Package) []PackageStatus {
	leftIdentities := identitySet(left)
	rightIdentities := identitySet(right)

	var statuses []PackageStatus

	if hasSetDifference(leftIdentities, rightIdentities) {
		statuses = append(statuses, PackageStatusMissingFromRight)
	}

	if len(left) == 0 && len(right) > 0 {
		statuses = append(statuses, PackageStatusAddedInRight)
	}

	if architecturesDiffer(left, right) {
		statuses = append(statuses, PackageStatusArchitecturesDiffer)
	}

	return statuses
}

func identitySet(packages []Package) map[string]struct{} {
	result := make(map[string]struct{}, len(packages))
	for _, pkg := range packages {
		result[pkg.Identity()] = struct{}{}
	}

	return result
}

func hasSetDifference(left, right map[string]struct{}) bool {
	for identity := range left {
		if _, ok := right[identity]; !ok {
			return true
		}
	}

	return false
}

func architecturesDiffer(left, right []Package) bool {
	leftArches := archesByVersionAndKind(left)
	rightArches := archesByVersionAndKind(right)

	for key, arches := range leftArches {
		if other, ok := rightArches[key]; ok && !equalStrings(arches, other) {
			return true
		}
	}

	return false
}

func archesByVersionAndKind(packages []Package) map[string][]string {
	sets := make(map[string]map[string]struct{})
	for _, pkg := range packages {
		key := pkg.EVR() + "\x00" + string(pkg.Kind)
		if sets[key] == nil {
			sets[key] = make(map[string]struct{})
		}

		sets[key][pkg.Arch] = struct{}{}
	}

	result := make(map[string][]string, len(sets))
	for key, set := range sets {
		for arch := range set {
			result[key] = append(result[key], arch)
		}

		sort.Strings(result[key])
	}

	return result
}

func sortedNEVRs(packages []Package) ([]string, error) {
	type entry struct {
		nevr    string
		version *rpm.Version
	}

	byNEVR := make(map[string]entry)

	for _, pkg := range packages {
		nevr := pkg.NEVR()
		if _, ok := byNEVR[nevr]; ok {
			continue
		}

		version, err := rpm.NewVersionFromEVR(normalizedEpoch(pkg.Epoch), pkg.Version, pkg.Release)
		if err != nil {
			return nil, fmt.Errorf("invalid EVR for %#q:\n%w", pkg.NEVRA(), err)
		}

		byNEVR[nevr] = entry{nevr: nevr, version: version}
	}

	entries := make([]entry, 0, len(byNEVR))
	for _, value := range byNEVR {
		entries = append(entries, value)
	}

	sort.Slice(entries, func(leftIndex, rightIndex int) bool {
		return entries[leftIndex].version.GreaterThan(entries[rightIndex].version)
	})

	result := make([]string, 0, len(entries))
	for _, value := range entries {
		result = append(result, value.nevr)
	}

	return result, nil
}

func groupByName(packages []Package) map[string][]Package {
	result := make(map[string][]Package)
	for _, pkg := range packages {
		result[pkg.Name] = append(result[pkg.Name], pkg)
	}

	return result
}

func unionNames(left, right map[string][]Package) []string {
	set := make(map[string]struct{}, len(left)+len(right))
	for name := range left {
		set[name] = struct{}{}
	}

	for name := range right {
		set[name] = struct{}{}
	}

	result := make([]string, 0, len(set))
	for name := range set {
		result = append(result, name)
	}

	sort.Strings(result)

	return result
}

func joinStatuses(statuses []PackageStatus) string {
	values := make([]string, 0, len(statuses))
	for _, status := range statuses {
		values = append(values, string(status))
	}

	return strings.Join(values, ", ")
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}

	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}

	return true
}
