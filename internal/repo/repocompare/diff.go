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
	// PackageStatusAddedInRight indicates that the right contains an identity absent from the left.
	PackageStatusAddedInRight PackageStatus = "added-in-right"
	// PackageStatusArchitecturesDiffer indicates that matching NEVRs have different architecture sets.
	PackageStatusArchitecturesDiffer PackageStatus = "architectures-differ"
)

// Options controls package inventory comparison behavior.
type Options struct {
	IgnoreOlderAddedInRight bool
}

// PackageReport summarizes both inventories for one differing package name.
type PackageReport struct {
	Name       string `json:"name"       table:"Name"`
	Summary    string `json:"summary"    table:"Summary"`
	LeftNEVRs  string `json:"leftNevrs"  table:"Left NEVRs"`
	RightNEVRs string `json:"rightNevrs" table:"Right NEVRs"`
}

// MissingPackage is a package version present on the left but absent from the right.
type MissingPackage struct {
	Name    string `json:"name"    table:"Name"`
	Version string `json:"version" table:"Version"`
}

// DiffStat summarizes package-level inventory differences.
type DiffStat struct {
	MissingFromRight    int `json:"missingFromRight"    table:"Missing from right"`
	AddedInRight        int `json:"addedInRight"        table:"Added in right"`
	ArchitecturesDiffer int `json:"architecturesDiffer" table:"Architectures differ"`
	Total               int `json:"total"               table:"Total"`
}

// MissingFromRightStat summarizes package versions missing from the right inventory.
type MissingFromRightStat struct {
	MissingFromRight int `json:"missingFromRight" table:"Missing from right"`
}

// Compare returns one package-centric report for each package name with inventory differences.
func Compare(left, right []Package) ([]PackageReport, error) {
	return CompareWithOptions(left, right, Options{})
}

// CompareWithOptions returns package-centric reports using the selected comparison behavior.
func CompareWithOptions(left, right []Package, options Options) ([]PackageReport, error) {
	leftByName := groupByName(left)
	rightByName := groupByName(right)
	names := unionNames(leftByName, rightByName)
	reports := make([]PackageReport, 0, len(names))

	for _, name := range names {
		leftPackages := leftByName[name]
		rightPackages := rightByName[name]

		statuses, err := packageStatuses(leftPackages, rightPackages, options)
		if err != nil {
			return nil, fmt.Errorf("comparing package %#q:\n%w", name, err)
		}

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

// Summarize returns package-level status and total counts.
func Summarize(reports []PackageReport) DiffStat {
	result := DiffStat{Total: len(reports)}

	for _, report := range reports {
		if report.hasStatus(PackageStatusMissingFromRight) {
			result.MissingFromRight++
		}

		if report.hasStatus(PackageStatusAddedInRight) {
			result.AddedInRight++
		}

		if report.hasStatus(PackageStatusArchitecturesDiffer) {
			result.ArchitecturesDiffer++
		}
	}

	return result
}

// MissingFromRight returns sorted package versions that occur only in the left inventory.
// Architectures and artifact kinds do not affect membership.
func MissingFromRight(left, right []Package) []MissingPackage {
	rightVersions := make(map[string]struct{}, len(right))
	for _, pkg := range right {
		rightVersions[packageVersionKey(pkg)] = struct{}{}
	}

	missingVersions := make(map[string]MissingPackage)

	for _, pkg := range left {
		key := packageVersionKey(pkg)
		if _, ok := rightVersions[key]; !ok {
			missingVersions[key] = MissingPackage{Name: pkg.Name, Version: pkg.EVR()}
		}
	}

	result := make([]MissingPackage, 0, len(missingVersions))
	for _, pkg := range missingVersions {
		result = append(result, pkg)
	}

	sort.Slice(result, func(leftIndex, rightIndex int) bool {
		if result[leftIndex].Name != result[rightIndex].Name {
			return result[leftIndex].Name < result[rightIndex].Name
		}

		return result[leftIndex].Version < result[rightIndex].Version
	})

	return result
}

func packageStatuses(left, right []Package, options Options) ([]PackageStatus, error) {
	leftIdentities := identitySet(left)
	rightIdentities := identitySet(right)

	var statuses []PackageStatus

	if hasSetDifference(leftIdentities, rightIdentities) {
		statuses = append(statuses, PackageStatusMissingFromRight)
	}

	hasRightAddition, err := hasUnignoredRightAddition(
		left, right, leftIdentities, options.IgnoreOlderAddedInRight,
	)
	if err != nil {
		return nil, err
	}

	if hasRightAddition {
		statuses = append(statuses, PackageStatusAddedInRight)
	}

	if architecturesDiffer(left, right) {
		statuses = append(statuses, PackageStatusArchitecturesDiffer)
	}

	return statuses, nil
}

func hasUnignoredRightAddition(
	left []Package,
	right []Package,
	leftIdentities map[string]struct{},
	ignoreOlder bool,
) (bool, error) {
	if !ignoreOlder {
		return hasSetDifference(identitySet(right), leftIdentities), nil
	}

	latestLeft, err := latestVersionsByKindAndArch(left)
	if err != nil {
		return false, err
	}

	for _, pkg := range right {
		if _, ok := leftIdentities[pkg.Identity()]; ok {
			continue
		}

		version, err := rpm.NewVersionFromEVR(normalizedEpoch(pkg.Epoch), pkg.Version, pkg.Release)
		if err != nil {
			return false, fmt.Errorf("invalid right EVR for %#q:\n%w", pkg.NEVRA(), err)
		}

		leftVersion, ok := latestLeft[kindAndArchKey(pkg)]
		if !ok || version.Compare(leftVersion) >= 0 {
			return true, nil
		}
	}

	return false, nil
}

func latestVersionsByKindAndArch(packages []Package) (map[string]*rpm.Version, error) {
	result := make(map[string]*rpm.Version)

	for _, pkg := range packages {
		version, err := rpm.NewVersionFromEVR(normalizedEpoch(pkg.Epoch), pkg.Version, pkg.Release)
		if err != nil {
			return nil, fmt.Errorf("invalid left EVR for %#q:\n%w", pkg.NEVRA(), err)
		}

		key := kindAndArchKey(pkg)

		current, ok := result[key]
		if !ok || version.GreaterThan(current) {
			result[key] = version
		}
	}

	return result, nil
}

func kindAndArchKey(pkg Package) string {
	return string(pkg.Kind) + "\x00" + pkg.Arch
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

func (r PackageReport) hasStatus(status PackageStatus) bool {
	for value := range strings.SplitSeq(r.Summary, ", ") {
		if value == string(status) {
			return true
		}
	}

	return false
}

func packageVersionKey(pkg Package) string {
	return pkg.Name + "\x00" + pkg.EVR()
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
