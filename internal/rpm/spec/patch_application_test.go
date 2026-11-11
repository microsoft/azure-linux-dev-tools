// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type patchApplicationTestCase struct {
	name         string
	declarations []string
	prep         []string
	content      []string
	number       int
	reason       string
	noPrep       bool
}

func TestAnalyzePatchApplications(t *testing.T) {
	tests := []patchApplicationTestCase{
		{name: "setup only warns", prep: []string{"%setup -q"}, reason: "no recognized command"},
		{name: "empty prep warns", reason: "no recognized command"},
		{name: "no prep warns", noPrep: true, reason: "no explicit"},
		{name: "disabled setup warns", prep: []string{"%autosetup -N -p1"}, reason: "no recognized command"},
		{name: "disabled forge warns", prep: []string{"%forgeautosetup -N"}, reason: "no recognized command"},
		{name: "go setup only warns", prep: []string{"%goprep"}, reason: "no recognized command"},
		{name: "autosetup", prep: []string{"%autosetup -p1"}},
		{name: "forge", prep: []string{"%forgeautosetup -p1"}},
		{name: "go", prep: []string{"%goprep -p1"}},
		{name: "disabled then bulk", prep: []string{"%autosetup -N", "%autopatch -p1"}},
		{name: "manual", prep: []string{"%setup -q", "%patch -P0 -p1 -b .backup"}},
		{name: "legacy", prep: []string{"%patch0 -p1"}},
		{name: "positional", prep: []string{"%patch 0 -p1"}},
		{name: "implicit zero", prep: []string{"%patch -p1"}},
		{name: "literal selection with shell comment", prep: []string{"%patch0 -p1 # backport; see issue"}},
		{name: "literal selection with quote inside comment", prep: []string{"%patch0 -p1 # author's backport; see issue"}},
		{name: "selected autopatch", prep: []string{"%autopatch -p1 0 5"}},
		{name: "selected ignores range", prep: []string{"%autopatch -p1 -m5 0"}},
		{name: "inclusive range", prep: []string{"%autopatch -m0 -M0 -p1"}},
		{name: "range excludes", prep: []string{"%autopatch -m1 -p1"}, reason: "no recognized"},
		{
			name: "upper range excludes", prep: []string{"%autopatch -M0 -p1"}, declarations: []string{"Patch1: fix.patch"},
			number: 1, reason: "no recognized",
		},
		{name: "individual excludes", prep: []string{"%patch -P1 -p1"}, reason: "no recognized"},
		{name: "positional excludes", prep: []string{"%patch 1 -p1"}, reason: "no recognized"},
		{name: "legacy excludes", prep: []string{"%patch1 -p1"}, reason: "no recognized"},
		{
			name: "implicit zero excludes", declarations: []string{"Patch1: fix.patch"},
			prep: []string{"%patch -p1"}, number: 1, reason: "no recognized",
		},
		{name: "range excludes then individual matches", prep: []string{"%autopatch -m1 -p1", "%patch -P0 -p1"}},
		{
			name: "different individual commands", declarations: []string{"Patch0: old.patch", "Patch1: fix.patch"},
			prep: []string{"%patch0 -p1", "%patch1 -p1"}, number: 1,
		},
		{name: "duplicate individual ignored", prep: []string{"%patch0 -p1", "%patch -P0 -p1"}},
		{name: "bulk and individual duplicate ignored", prep: []string{"%autosetup -p1", "%patch0 -p1"}},
		{name: "macro range", prep: []string{"%autopatch -M%{limit} -p1"}, reason: "no recognized command"},
		{name: "macro strip", prep: []string{"%patch -P0 -p%{strip}"}, reason: "macro-valued"},
		{name: "macro named setup", prep: []string{"%autosetup -n %{name}-%{version} -p1"}},
		{name: "macro setup then explicit", prep: []string{"%autosetup -N -n %{name}", "%patch0 -p1"}},
		{name: "python blivet", prep: []string{
			"%autosetup -n %{realname}-%{realversion} -N", "%autosetup -n %{realname}-%{realversion} -b1 -p1",
		}, reason: "multi-source"},
		{name: "multi-source setup", prep: []string{"%autosetup -b1 -p1"}, reason: "multi-source"},
		{name: "multiple setups", prep: []string{"%setup -q", "%autosetup -p1"}},
	}
	runPatchApplicationTests(t, tests)
}

func TestAnalyzePatchApplicationDeclarations(t *testing.T) {
	tests := []patchApplicationTestCase{
		{name: "conditional application", prep: []string{"%ifarch x86_64", "%patch0 -p1", "%endif"}, reason: "conditional"},
		{
			name:         "closed conditional before prep",
			declarations: []string{"%ifarch x86_64", "Summary: test", "%endif", "Patch0: fix.patch"},
			prep:         []string{"%patch0 -p1"},
		},
		{
			name: "conditional prep", declarations: []string{"Patch0: fix.patch", "%ifarch x86_64"},
			prep: []string{"%patch0 -p1", "%endif"}, reason: "conditional",
		},
		{name: "repeated prep", prep: []string{"%prep", "%patch0 -p1"}, reason: "multiple"},
		{name: "augmented prep", prep: []string{"%prep -a", "%patch0 -p1"}, reason: "nonstandard"},
		{
			name:         "uv unsupported prep does not advise duplicate application",
			declarations: []string{"BuildSystem: pyproject", "Patch0: fix.patch"}, prep: []string{"%prep -a", "echo extra"},
			reason: "nonstandard",
		},
		{name: "unbalanced conditional", prep: []string{"%ifarch x86_64", "%patch0 -p1"}, reason: "unbalanced"},
		{
			name: "conditional declaration", declarations: []string{"%ifarch x86_64", "Patch0: fix.patch", "%endif"},
			prep: []string{"%patch0 -p1"}, reason: "conditional",
		},
		{
			name: "duplicate declaration number", declarations: []string{"Patch0: old.patch", "Patch0: fix.patch"},
			prep: []string{"%patch0 -p1"}, reason: "also used",
		},
		{
			name: "duplicate declaration filename", declarations: []string{"Patch0: fix.patch", "Patch1: fix.patch"},
			prep: []string{"%autopatch -p1"}, reason: "multiple",
		},
		{
			name: "unnumbered declaration", declarations: []string{"Patch4: old.patch", "Patch: fix.patch"},
			prep: []string{"%patch5 -p1"}, number: 5,
		},
		{
			name:         "lower explicit number does not reset bare numbering",
			declarations: []string{"Patch10: old.patch", "Patch1: other.patch", "Patch: fix.patch"},
			prep:         []string{"%patch -P11 -p1"},
		},
		{
			name:         "lower explicit number does not reset patchlist numbering",
			declarations: []string{"Patch10: old.patch", "Patch1: other.patch", "%patchlist", "fix.patch"},
			prep:         []string{"%patch -P11 -p1"},
		},
		{
			name:         "lower explicit number cannot select reset automatic number",
			declarations: []string{"Patch10: old.patch", "Patch1: other.patch", "%patchlist", "fix.patch"},
			prep:         []string{"%patch -P2 -p1"}, number: 11, reason: "no recognized",
		},
		{
			name:         "automatic entries continue above highest assigned number",
			declarations: []string{"Patch10: old.patch", "Patch1: other.patch", "Patch: auto.patch", "%patchlist", "fix.patch"},
			prep:         []string{"%patch -P12 -p1"},
		},
		{
			name:         "lower explicit number preserves conditional numbering uncertainty",
			declarations: []string{"%ifarch x86_64", "Patch10: old.patch", "%endif", "Patch1: other.patch", "Patch: fix.patch"},
			prep:         []string{"%patch -P2 -p1"}, number: -1, reason: "numbering is unknown",
		},
		{
			name: "lower explicit number preserves conditional patchlist uncertainty",
			declarations: []string{
				"%ifarch x86_64", "Patch10: old.patch", "%endif", "Patch1: other.patch", "%patchlist", "fix.patch",
			},
			prep: []string{"%patch -P11 -p1"}, number: -1, reason: "numbering is unknown",
		},
		{
			name:         "explicit number preserves opaque numbering uncertainty",
			declarations: []string{"Patch%{n}: old.patch", "Patch1: other.patch", "Patch: fix.patch"},
			prep:         []string{"%patch -P2 -p1"}, number: -1, reason: "numbering is unknown",
		},
		{
			name:         "conditional declaration makes later bare number uncertain",
			declarations: []string{"%ifarch x86_64", "Patch: old.patch", "%endif", "Patch: fix.patch"},
			prep:         []string{"%patch1 -p1"}, number: -1, reason: "numbering is unknown",
		},
		{
			name:         "conditional declaration makes later patchlist number uncertain",
			declarations: []string{"%ifarch x86_64", "Patch: old.patch", "%endif", "%patchlist", "fix.patch"},
			prep:         []string{"%patch1 -p1"}, number: -1, reason: "numbering is unknown",
		},
		{
			name:         "fixed declaration stays identifiable after conditional declarations",
			declarations: []string{"%ifarch x86_64", "Patch: old.patch", "%endif", "Patch10: fix.patch"},
			prep:         []string{"%patch10 -p1"}, number: 10,
		},
		{
			name: "unknown automatic number may collide with fixed target",
			declarations: []string{
				"%ifarch x86_64", "Patch4: conditional.patch", "%endif", "Patch: auto.patch", "Patch0: fix.patch",
			},
			prep: []string{"%patch0 -p1"},
		},
		{
			name: "patchlist", declarations: []string{"Patch4: old.patch", "%patchlist", "# comment", "fix.patch"},
			prep: []string{"%patch5 -p1"}, number: 5,
		},
		{
			name: "macro numbering", declarations: []string{"Patch%{n}: old.patch", "Patch0: fix.patch"},
			prep: []string{"%patch0 -p1"},
		},
		{
			name:         "generated declaration",
			declarations: []string{"%{lua:", "print('Patch4: old.patch')", "}", "Patch0: fix.patch"},
			prep:         []string{"%patch0 -p1"},
		},
	}
	runPatchApplicationTests(t, tests)
}

func TestAnalyzePatchApplicationLexicalBoundaries(t *testing.T) {
	tests := []patchApplicationTestCase{
		{name: "opaque prep", prep: []string{"./prepare.sh"}, reason: "no recognized command"},
		{
			name: "opaque manual patch loop",
			prep: []string{"for p in %patches; do", "patch -p1 < $p", "done"}, reason: "no recognized command",
		},
		{name: "conditional expansion", prep: []string{"%{?apply:%patch0 -p1}"}, reason: "no recognized command"},
		{name: "inline patch call", prep: []string{"echo prepare; %patch0 -p1"}, reason: "no recognized command"},
		{name: "commented call", prep: []string{"# %patch0 -p1"}, reason: "no recognized"},
		{
			name: "macro definition not a call",
			prep: []string{"%define unused() \\", "%patch0 -p1", "%setup -q"}, reason: "no recognized",
		},
		{name: "macro body skipped", prep: []string{"%define unused() %{expand:", "%patch0 -p2", "}", "%patch0 -p1"}},
		{
			name: "inline expansion body is not an explicit call",
			prep: []string{"echo %{expand:", "%patch0 -p1", "}"}, reason: "no recognized command",
		},
		{
			name: "inline nested expansion body is not an explicit call",
			prep: []string{"echo %{?custom:%{expand:", "%patch0 -p1", "}}"}, reason: "no recognized command",
		},
		{
			name: "inline Lua expansion body is not an explicit call",
			prep: []string{"echo %{lua:", "print('expanded')", "%patch0 -p1", "}"}, reason: "no recognized command",
		},
		{name: "unterminated macro", prep: []string{"%define unused() %{expand:"}, reason: "unterminated"},
		{
			name: "loaded macros ignored", declarations: []string{"%{load:external.macros}", "Patch0: fix.patch"},
			prep: []string{"%patch0 -p1"},
		},
		{
			name: "multiline load boundary", declarations: []string{"%{load:", "external.macros", "}", "Patch0: fix.patch"},
			prep: []string{"%autopatch -p1"},
		},
		{
			name: "load filename mentions patch", declarations: []string{"%{load:pkg-patch.macros}", "Patch0: fix.patch"},
			prep: []string{"%autopatch -p1"},
		},
		{
			name: "load with nested macro filename", declarations: []string{"%{load:%{name}.macros}", "Patch0: fix.patch"},
			prep: []string{"%autopatch -p1"},
		},
		{
			name: "visible expansion after load",
			prep: []string{"%{load:external.macros}%{apply_fixes}"}, reason: "no recognized command",
		},
		{
			name: "inline override", declarations: []string{"%define patch() echo skipped", "Patch0: fix.patch"},
			prep: []string{"%patch -P0 -p1"}, reason: "redefined",
		},
		{
			name: "inline undefine", declarations: []string{"%undefine autopatch", "Patch0: fix.patch"},
			prep: []string{"%autopatch -p1"}, reason: "redefined",
		},
		{
			name:         "conditional override",
			declarations: []string{"%ifarch x86_64", "%define patch() echo skipped", "%endif", "Patch0: fix.patch"},
			prep:         []string{"%patch -P0 -p1"},
		},
		{
			name: "expansion override", declarations: []string{"%{?custom:%global __patch /bin/true}", "Patch0: fix.patch"},
			prep: []string{"%patch -P0 -p1"},
		},
		{
			name: "custom backend definition", declarations: []string{"%global __scm git", "Patch0: fix.patch"},
			prep: []string{"%autosetup -p1"}, reason: "redefined",
		},
		{
			name: "patch helper override", declarations: []string{"%global __patch /bin/true", "Patch0: fix.patch"},
			prep: []string{"%patch -P0 -p1"}, reason: "redefined",
		},
		{
			name:         "direct patch ignores bulk helper",
			declarations: []string{"%global __scm_apply_patch /bin/true", "Patch0: fix.patch"},
			prep:         []string{"%patch -P0 -p1"},
		},
		{
			name: "git ignores patch helper", declarations: []string{"%global __patch /bin/true", "Patch0: fix.patch"},
			prep: []string{"%autosetup -S git -p1"},
		},
		{
			name: "prep hook", declarations: []string{"%global __spec_prep_pre apply_fixes", "Patch0: fix.patch"},
			prep: []string{"%patch0 -p1"}, reason: "hooks",
		},
		{
			name: "macro setup override affects wrapper", declarations: []string{"%undefine autosetup", "Patch0: fix.patch"},
			prep: []string{"%forgeautosetup -p1"}, reason: "redefined",
		},
	}
	runPatchApplicationTests(t, tests)
}

func TestAnalyzePatchApplicationHelperDependencies(t *testing.T) {
	tests := []patchApplicationTestCase{
		{
			name: "bulk apply helper override",
			prep: []string{"%global __apply_patch(qp:m:) echo skipped", "%autopatch -p1"}, reason: "redefined",
		},
		{
			name: "autosetup apply helper override",
			prep: []string{"%undefine __apply_patch", "%autosetup -p1"}, reason: "redefined",
		},
		{
			name: "git bulk apply helper override",
			prep: []string{"%undefine __apply_patch", "%autosetup -S git -p1"}, reason: "redefined",
		},
		{
			name: "forge apply helper override",
			prep: []string{"%undefine __apply_patch", "%forgeautosetup -p1"}, reason: "redefined",
		},
		{
			name: "go apply helper override",
			prep: []string{"%undefine __apply_patch", "%goprep -p1"}, reason: "redefined",
		},
		{
			name: "manual patch ignores bulk apply helper",
			prep: []string{"%undefine __apply_patch", "%patch -P0 -p1"},
		},
		{
			name: "patch backend setup helper override",
			prep: []string{"%global __scm_setup_patch exit 0", "%autosetup -p1"}, reason: "redefined",
		},
		{
			name: "git backend setup helper override",
			prep: []string{"%undefine __scm_setup_git", "%autosetup -S git -p1"}, reason: "redefined",
		},
		{
			name: "forge backend setup helper override",
			prep: []string{"%undefine __scm_setup_patch", "%forgeautosetup -p1"}, reason: "redefined",
		},
		{
			name: "go backend setup helper override",
			prep: []string{"%undefine __scm_setup_patch", "%goprep -p1"}, reason: "redefined",
		},
		{
			name: "manual patch ignores backend setup helper",
			prep: []string{"%undefine __scm_setup_patch", "%patch0 -p1"},
		},
		{
			name: "autopatch ignores backend setup helper",
			prep: []string{"%undefine __scm_setup_patch", "%autopatch -p1"},
		},
		{
			name: "patch backend ignores git setup helper",
			prep: []string{"%undefine __scm_setup_git", "%autosetup -p1"},
		},
		{
			name: "git backend ignores patch setup helper",
			prep: []string{"%undefine __scm_setup_patch", "%autosetup -S git -p1"},
		},
		{
			name: "unrelated backend apply helper ignored",
			prep: []string{"%undefine __scm_apply_git", "%autosetup -p1"},
		},
		{
			name: "unrelated helper ignored",
			prep: []string{"%undefine __unrelated", "%autosetup -p1"},
		},
		{
			name: "conditional bulk helper override ignored",
			prep: []string{"%ifarch x86_64", "%undefine __apply_patch", "%endif", "%autopatch -p1"},
		},
		{
			name: "conditional setup helper override ignored",
			prep: []string{"%ifarch x86_64", "%undefine __scm_setup_patch", "%endif", "%autosetup -p1"},
		},
		{
			name: "later bulk helper override does not invalidate selection",
			prep: []string{"%autopatch -p1", "%undefine __apply_patch", "%autopatch -p1"},
		},
		{
			name: "later setup helper override does not invalidate selection",
			prep: []string{"%autosetup -p1", "%undefine __scm_setup_patch", "%autosetup -p1"},
		},
		{
			name: "manual candidate remains uncertain after invoked bulk helper override",
			prep: []string{"%undefine __apply_patch", "%autopatch -p1", "%patch0 -p1"}, reason: "redefined",
		},
	}
	runPatchApplicationTests(t, tests)
}

func TestAnalyzePatchApplicationHelperExecution(t *testing.T) {
	tests := []patchApplicationTestCase{
		{
			name:   "bulk helper can terminate later application of a different patch",
			prep:   []string{"%global __apply_patch(qp:m:) exit 0", "%autopatch -p1 1", "%patch -P0 -p1"},
			reason: "redefined",
		},
		{
			name:   "backend apply helper can terminate later application",
			prep:   []string{"%global __scm_apply_patch exit 0", "%autopatch -p1 1", "%patch0 -p1"},
			reason: "redefined",
		},
		{
			name:   "patch executable override can terminate later application",
			prep:   []string{"%global __patch exit 0", "%patch1 -p1", "%patch0 -p1"},
			reason: "redefined",
		},
		{
			name:   "patch flags override can terminate later application",
			prep:   []string{"%global _default_patch_flags ; exit 0 #", "%patch1 -p1", "%patch0 -p1"},
			reason: "redefined",
		},
		{
			name:   "bulk macro override can terminate later application",
			prep:   []string{"%define autopatch() exit 0", "%autopatch -p1 1", "%patch0 -p1"},
			reason: "redefined",
		},
		{
			name:   "manual macro override can terminate later bulk application",
			prep:   []string{"%define patch() exit 0", "%patch -P1 -p1", "%autopatch -p1 0"},
			reason: "redefined",
		},
		{
			name: "earlier sound manual candidate remains valid",
			prep: []string{"%patch0 -p1", "%global __apply_patch(qp:m:) exit 0", "%autopatch -p1 1"},
		},
		{
			name: "earlier sound bulk candidate remains valid",
			prep: []string{"%autopatch -p1 0", "%global __apply_patch(qp:m:) exit 0", "%autopatch -p1 1"},
		},
		{
			name: "uninvoked bulk helper does not affect manual candidates",
			prep: []string{"%global __apply_patch(qp:m:) exit 0", "%patch1 -p1", "%patch0 -p1"},
		},
		{
			name: "uninvoked backend helper does not affect manual candidates",
			prep: []string{"%global __scm_apply_patch exit 0", "%patch1 -p1", "%patch0 -p1"},
		},
		{
			name: "disabled application helper does not affect later manual candidate",
			prep: []string{"%global __apply_patch(qp:m:) exit 0", "%autosetup -N", "%patch0 -p1"},
		},
		{
			name: "disabled application macro does not affect later manual candidate",
			prep: []string{"%define autopatch() exit 0", "%autosetup -N", "%patch0 -p1"},
		},
		{
			name: "unrelated backend helper does not affect later manual candidate",
			prep: []string{"%global __scm_apply_git exit 0", "%autopatch -p1 1", "%patch0 -p1"},
		},
		{
			name: "empty bulk selection does not invoke application helpers",
			prep: []string{"%global __apply_patch(qp:m:) exit 0", "%autopatch -p1 -m2", "%patch0 -p1"},
		},
		{
			name: "empty bulk selection does not invoke patch executable",
			prep: []string{"%global __patch exit 0", "%autopatch -p1 -m2", "%autosetup -S git -p1"},
		},
		{
			name:   "empty bulk selection still invokes overridden bulk macro",
			prep:   []string{"%define autopatch() exit 0", "%autopatch -p1 -m2", "%patch0 -p1"},
			reason: "redefined",
		},
		{
			name: "conditional helper invocation does not affect later manual candidate",
			prep: []string{
				"%global __apply_patch(qp:m:) exit 0", "%ifarch x86_64", "%autopatch -p1 1", "%endif", "%patch0 -p1",
			},
		},
		{
			name: "conditional helper definition does not affect later candidates",
			prep: []string{
				"%ifarch x86_64", "%global __apply_patch(qp:m:) exit 0", "%endif", "%autopatch -p1 1", "%patch0 -p1",
			},
		},
	}
	for index := range tests {
		tests[index].declarations = []string{"Patch0: fix.patch", "Patch1: other.patch"}
	}

	runPatchApplicationTests(t, tests)
}

func TestAnalyzePatchApplicationSetupExecution(t *testing.T) {
	tests := []patchApplicationTestCase{
		{
			name: "disabled setup helper can terminate later manual application",
			prep: []string{
				"%global __scm_setup_patch(q) exit 0", "%autosetup -c -T -N", "%patch -P0 -p1",
			},
			reason: "redefined patch application",
		},
		{
			name:   "disabled setup helper can terminate later bulk application",
			prep:   []string{"%undefine __scm_setup_patch", "%autosetup -N", "%autopatch -p1"},
			reason: "redefined patch application",
		},
		{
			name:   "plain setup override affects later application",
			prep:   []string{"%define setup() exit 0", "%setup -q", "%patch0 -p1"},
			reason: "redefined patch application",
		},
		{
			name:   "autosetup override affects later application",
			prep:   []string{"%define autosetup() exit 0", "%autosetup -N", "%patch0 -p1"},
			reason: "redefined patch application",
		},
		{
			name:   "forge setup override affects later application",
			prep:   []string{"%undefine __scm_setup_patch", "%forgeautosetup -N", "%patch0 -p1"},
			reason: "redefined patch application",
		},
		{
			name:   "go setup override affects later application",
			prep:   []string{"%undefine gomkdir", "%goprep", "%patch0 -p1"},
			reason: "redefined patch application",
		},
		{
			name:   "later setup cannot restore known execution",
			prep:   []string{"%undefine __scm_setup_patch", "%autosetup -N", "%setup -q", "%patch0 -p1"},
			reason: "redefined patch application",
		},
		{
			name: "earlier manual selection remains valid",
			prep: []string{"%patch0 -p1", "%undefine __scm_setup_patch", "%autosetup -N", "%patch0 -p1"},
		},
		{
			name: "uninvoked setup helper override is irrelevant",
			prep: []string{"%undefine __scm_setup_patch", "%setup -q", "%patch0 -p1"},
		},
		{
			name: "unrelated backend setup helper override is irrelevant",
			prep: []string{"%undefine __scm_setup_git", "%autosetup -N", "%patch0 -p1"},
		},
		{
			name: "disabled bulk helper override does not affect setup",
			prep: []string{"%undefine __apply_patch", "%autosetup -N", "%patch0 -p1"},
		},
		{
			name: "disabled backend apply override does not affect setup",
			prep: []string{"%undefine __scm_apply_patch", "%autosetup -N", "%patch0 -p1"},
		},
		{
			name: "conditional setup invocation does not affect later application",
			prep: []string{
				"%undefine __scm_setup_patch", "%ifarch x86_64", "%autosetup -N", "%endif", "%patch0 -p1",
			},
		},
		{
			name: "conditional setup override does not affect later application",
			prep: []string{
				"%ifarch x86_64", "%undefine __scm_setup_patch", "%endif", "%autosetup -N", "%patch0 -p1",
			},
		},
	}
	runPatchApplicationTests(t, tests)
}

func TestAnalyzePatchApplicationGitHelperDependencies(t *testing.T) {
	tests := []patchApplicationTestCase{
		{
			name: "git executable override affects autosetup",
			prep: []string{"%global __git /bin/true", "%autosetup -c -T -S git -p1"}, reason: "redefined",
		},
		{
			name:   "git executable override affects inherited bulk application",
			prep:   []string{"%autosetup -N -S git", "%global __git /bin/true", "%autopatch -p1"},
			reason: "redefined",
		},
		{
			name:   "git executable override affects disabled source setup",
			prep:   []string{"%global __git /bin/true", "%autosetup -N -S git", "%patch0 -p1"},
			reason: "redefined patch application",
		},
		{
			name: "git executable override affects forge",
			prep: []string{"%undefine __git", "%forgeautosetup -S git -p1"}, reason: "redefined",
		},
		{
			name: "git executable override does not affect patch autosetup",
			prep: []string{"%global __git /bin/true", "%autosetup -p1"},
		},
		{
			name: "git executable override does not affect patch autopatch",
			prep: []string{"%global __git /bin/true", "%autopatch -p1"},
		},
		{
			name: "git executable override does not affect plain setup",
			prep: []string{"%global __git /bin/true", "%setup -q", "%patch0 -p1"},
		},
		{
			name: "manual patch ignores inherited git backend",
			prep: []string{"%autosetup -N -S git", "%global __git /bin/true", "%patch0 -p1"},
		},
		{
			name: "conditional git executable override ignored",
			prep: []string{"%ifarch x86_64", "%undefine __git", "%endif", "%autosetup -S git -p1"},
		},
		{
			name: "earlier git selection remains valid",
			prep: []string{"%autosetup -S git -p1", "%undefine __git", "%autosetup -S git -p1"},
		},
		{
			name:   "manual candidate remains uncertain after invoked git application override",
			prep:   []string{"%autosetup -N -S git", "%undefine __git", "%autopatch -p1", "%patch0 -p1"},
			reason: "redefined",
		},
	}
	runPatchApplicationTests(t, tests)
}

func TestAnalyzePatchApplicationCommandsAndPaths(t *testing.T) {
	tests := []patchApplicationTestCase{
		{name: "unknown option", prep: []string{"%patch -P0 --unknown"}, reason: "no recognized command"},
		{name: "missing option value", prep: []string{"%patch -P"}, reason: "no recognized command"},
		{name: "negative number", prep: []string{"%patch -P-1"}, reason: "no recognized command"},
		{name: "conflicting individual selections", prep: []string{"%patch1 -P2 -p1"}, reason: "no recognized command"},
		{name: "malformed quote", prep: []string{"%patch -P0 -p'1"}, reason: "shell control"},
		{name: "shell option value", prep: []string{"%patch -P0 -b`run`"}, reason: "shell"},
		{name: "continued command", prep: []string{"%patch -P0 \\", "-p1"}, reason: "continued"},
		{name: "unknown backend", prep: []string{"%autosetup -S custom"}, reason: "backend"},
		{name: "git default strip", prep: []string{"%autosetup -S git"}},
		{name: "backend carries to autopatch", prep: []string{"%autosetup -N -S git", "%autopatch"}},
		{name: "strip zero mismatch", prep: []string{"%patch0 -p0"}, reason: "uses '-p0'"},
		{name: "strip two mismatch", prep: []string{"%patch0 -p2"}, reason: "uses '-p2'"},
		{name: "omitted strip nested path", prep: []string{"%patch0"}, reason: "omits '-p'"},
		{name: "omitted strip root git path", prep: []string{"%patch0"}, content: []string{"--- a/file.c", "+++ b/file.c"}},
		{
			name: "strip entire path", prep: []string{"%patch0 -p1"},
			content: []string{"--- file.c", "+++ file.c"}, reason: "entire",
		},
		{
			name: "plain paths strip zero", prep: []string{"%patch0 -p0"},
			content: []string{"--- lib/file.c", "+++ lib/file.c"},
		},
		{
			name: "quoted path", prep: []string{"%patch0 -p1"},
			content: []string{`--- "a/lib/my file.c"`, `+++ "b/lib/my file.c"`},
		},
		{name: "new file", prep: []string{"%patch0 -p1"}, content: []string{"--- /dev/null", "+++ b/lib/file.c"}},
		{
			name: "go vendor removed", prep: []string{"%goprep -p1"},
			content: []string{"--- a/vendor/lib/file.c", "+++ b/vendor/lib/file.c"},
			reason:  "removes vendor",
		},
		{
			name: "go vendor kept", prep: []string{"%goprep -k -p1"},
			content: []string{"--- a/vendor/lib/file.c", "+++ b/vendor/lib/file.c"},
		},
	}

	runPatchApplicationTests(t, tests)
}

func runPatchApplicationTests(t *testing.T, tests []patchApplicationTestCase) {
	t.Helper()

	for _, mode := range []spec.EditorMode{spec.EditorLegacy, spec.EditorStructural} {
		t.Run(string(mode), func(t *testing.T) {
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					declarations := test.declarations
					if declarations == nil {
						declarations = []string{"Patch0: fix.patch"}
					}

					lines := slices.Concat([]string{"Name: test"}, declarations)
					if !test.noPrep {
						lines = append(lines, "%prep")
					}

					lines = slices.Concat(lines, test.prep, []string{"%build", "Patch99: fix.patch", "%{?unused:echo patch}", "make"})
					input := strings.Join(lines, "\n") + "\n"
					opened, err := spec.OpenSpec(strings.NewReader(input), spec.WithEditor(mode))
					require.NoError(t, err)

					content := test.content
					if content == nil {
						content = []string{"--- a/lib/file.c", "+++ b/lib/file.c"}
					}

					diagnostics, err := opened.AnalyzePatchApplications([]spec.AddedPatch{{
						Filename: "fix.patch", Content: []byte(strings.Join(content, "\n") + "\n"),
					}})
					require.NoError(t, err)

					if test.reason == "" {
						assert.Empty(t, diagnostics)
					} else {
						require.Len(t, diagnostics, 1)
						assert.Equal(t, "fix.patch", diagnostics[0].Filename)
						assert.Equal(t, test.number, diagnostics[0].PatchNumber)
						assert.Contains(t, diagnostics[0].Reason, test.reason)
					}

					var serialized bytes.Buffer
					require.NoError(t, opened.Serialize(&serialized))
					assert.Equal(t, input, serialized.String(), "analysis must be read-only")
				})
			}
		})
	}
}

func TestAnalyzePatchApplicationsExplicitMatch(t *testing.T) {
	llvm := []string{
		"%{?compat:%{expand:echo prepare}} \\",
		"%if %{with bundle_compat_lib}",
		"%setup -T -b3000 -n %{compat_source}",
		"%autopatch -M499 -p1",
		"%autopatch -m%{compat_maj_ver}00 -M%{compat_maj_ver}99 -p1",
		"%endif",
		"%autosetup -N -T -b0 -n %{main_source}",
		"%autopatch -M499 -p1",
		"%autopatch -m%{maj_ver}00 -M%{maj_ver}99 -p1",
	}

	tests := []struct {
		name string
		prep []string
		warn bool
	}{
		{name: "LLVM safe explicit", prep: append(slices.Clone(llvm), "%patch -p1 -P2202")},
		{name: "LLVM without explicit", prep: llvm, warn: true},
		{name: "LLVM different patch", prep: append(slices.Clone(llvm), "%patch -p1 -P22020"), warn: true},
		{
			name: "LLVM conditional explicit",
			prep: slices.Concat(llvm, []string{"%ifarch x86_64", "%patch -p1 -P2202", "%endif"}), warn: true,
		},
		{name: "LLVM unsafe strip", prep: append(slices.Clone(llvm), "%patch -p0 -P2202"), warn: true},
		{name: "explicit before opaque range", prep: []string{"%patch -P2202 -p1", "%autopatch -M%{limit} -p1"}},
		{
			name: "quoted operators are not shell context",
			prep: []string{`echo 'prepare; cd lib' "&& exit 0"`, "%patch2202 -p1"},
		},
		{
			name: "escaped operators are not shell context",
			prep: []string{`echo prepare\;`, "%patch2202 -p1"},
		},
		{
			name: "commented operators are not shell context",
			prep: []string{"echo prepare # cd lib; exit 0 '", "%patch2202 -p1"},
		},
		{name: "explicit after opaque range", prep: []string{"%autopatch -M%{limit} -p1", "%patch -P2202 -p1"}},
		{name: "explicit selected autopatch", prep: []string{"%autopatch -M%{limit}", "%autopatch -p1 2202"}},
		{name: "legacy selection", prep: []string{"%autopatch -M%{limit}", "%patch2202 -p1"}},
		{name: "positional selection", prep: []string{"%autopatch -M%{limit}", "%patch 2202 -p1"}},
		{name: "bulk overrides opaque selection", prep: []string{"%autopatch -M%{limit}", "%autopatch -p1"}},
		{
			name: "multiple disabled setups do not veto explicit selection",
			prep: []string{"%setup -q", "%autosetup -N", "%patch -P2202 -p1"},
		},
		{
			name: "conditional block before safe command",
			prep: []string{"%ifarch x86_64", "%patch0 -p1", "%endif", "%patch2202 -p1"},
		},
		{
			name: "conditional override ignored",
			prep: []string{"%ifarch x86_64", "%undefine patch", "%endif", "%patch -P2202 -p1"},
		},
		{name: "working directory remains uncertain", prep: []string{"cd lib", "%patch -P2202 -p1"}, warn: true},
		{
			name: "directory in chained shell command remains uncertain",
			prep: []string{"echo prepare; cd lib", "%patch2202 -p1"}, warn: true,
		},
		{
			name: "exit in chained shell command remains uncertain",
			prep: []string{"echo prepare; exit 0", "%patch2202 -p1"}, warn: true,
		},
		{
			name: "directory with multiline expansion remains uncertain",
			prep: []string{"cd %{expand:", "lib", "}", "%patch2202 -p1"}, warn: true,
		},
		{
			name: "directory after closed expansion remains uncertain",
			prep: []string{"%{echo prepare}; cd lib", "%patch2202 -p1"}, warn: true,
		},
		{
			name: "conditional directory ignored",
			prep: []string{"%ifarch x86_64", "cd lib", "%endif", "%patch -P2202 -p1"},
		},
		{
			name: "conditional shell context ignored",
			prep: []string{"%ifarch x86_64", "if test -d lib; then", "%endif", "%patch -P2202 -p1"},
		},
		{
			name: "conditional here document ignored",
			prep: []string{"%ifarch x86_64", "cat <<EOF", "%endif", "%patch -P2202 -p1"},
		},
		{
			name: "shell context remains uncertain",
			prep: []string{"if test -d lib; then", "%patch -P2202 -p1", "fi"}, warn: true,
		},
		{name: "here document remains uncertain", prep: []string{"cat <<EOF", "%patch -P2202 -p1", "EOF"}, warn: true},
		{name: "matching macro arguments remain uncertain", prep: []string{"%patch -P2202 -p%{strip}"}, warn: true},
		{name: "macro strip before literal selection", prep: []string{"%patch -p%{strip} -P2202"}, warn: true},
		{
			name: "later directory hazard does not invalidate selection",
			prep: []string{"%patch2202 -p1", "cd lib", "%patch2202 -p1"},
		},
		{
			name: "later macro strip does not invalidate selection",
			prep: []string{"%patch2202 -p1", "%patch -p%{strip} -P2202"},
		},
		{
			name: "exit makes explicit command unreachable",
			prep: []string{"exit 0", "%patch2202 -p1"}, warn: true,
		},
		{
			name: "exec replaces shell before explicit command",
			prep: []string{"exec true", "%patch2202 -p1"}, warn: true,
		},
		{
			name: "known duplicates ignored",
			prep: []string{"%patch2202 -p1", "%patch -P2202 -p1", "%autopatch -M%{limit}"},
		},
	}

	for _, mode := range []spec.EditorMode{spec.EditorLegacy, spec.EditorStructural} {
		t.Run(string(mode), func(t *testing.T) {
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					lines := slices.Concat([]string{"Name: test", "Patch2202: fix.patch", "%prep"}, test.prep)
					opened, err := spec.OpenSpec(strings.NewReader(strings.Join(lines, "\n")+"\n"), spec.WithEditor(mode))
					require.NoError(t, err)
					diagnostics, err := opened.AnalyzePatchApplications([]spec.AddedPatch{{
						Filename: "fix.patch", Content: []byte("--- a/lib/file.c\n+++ b/lib/file.c\n"),
					}})
					require.NoError(t, err)

					if test.warn {
						require.Len(t, diagnostics, 1)
					} else {
						assert.Empty(t, diagnostics)
					}
				})
			}
		})
	}
}

func TestAnalyzePatchApplicationsUnconditionalSelection(t *testing.T) {
	tests := []patchApplicationTestCase{
		{
			name: "standard preamble macros do not invalidate bulk application",
			declarations: []string{
				"%bcond_without tests", "%gometa", "%gopkg", "Patch0: fix.patch",
			},
			prep: []string{"%autosetup -n %{name}-%{version} -p1"},
		},
		{
			name: "opaque commands before and after bulk application",
			prep: []string{"cp %{SOURCE1} .", "%autopatch -p1", "autoreconf -i"},
		},
		{
			name: "signature verification before bulk application",
			prep: []string{"%if %{verify_signature}", "%{gpgverify} --data=%{SOURCE0}", "%endif", "%autosetup -p1"},
		},
		{
			name: "signature expansion before bulk application",
			prep: []string{"%{gpgverify} --data=%{SOURCE0}", "%autosetup -p1"},
		},
		{
			name: "conditional cleanup after bulk application",
			prep: []string{"%autosetup -p1", "%if %{with feature}", "rm src/unused.c", "%endif"},
		},
		{
			name: "pipeline after bulk application",
			prep: []string{"%autosetup -p1", `find . -name '*.py' | xargs sed -i 's/old/new/'`},
		},
		{
			name: "later directory change does not invalidate application",
			prep: []string{"%autopatch -p1", "cd lib"},
		},
		{
			name: "later definition does not invalidate application",
			prep: []string{"%autopatch -p1", "%undefine autopatch"},
		},
		{
			name:   "definition between commands affects only later candidates",
			prep:   []string{"%patch1 -p1", "%undefine patch", "%patch0 -p1"},
			reason: "redefined",
		},
		{
			name: "unrelated generated definitions do not invalidate application",
			declarations: []string{
				"%{!?gopath:%global gopath %{_datadir}/gocode}", "Patch0: fix.patch",
			},
			prep: []string{"%autosetup -p1", "%files", "%{!?_licensedir:%global license %%doc}"},
		},
		{
			name: "unknown automatic number with bulk application",
			declarations: []string{
				"%ifarch x86_64", "Patch: old.patch", "%endif", "Patch: fix.patch",
			},
			prep: []string{"%autopatch -p1"},
		},
		{
			name: "unknown patchlist number with bulk application",
			declarations: []string{
				"%ifarch x86_64", "Patch: old.patch", "%endif", "%patchlist", "fix.patch",
			},
			prep: []string{"%autosetup -p1"},
		},
		{
			name: "unknown number cannot satisfy limited range",
			declarations: []string{
				"%ifarch x86_64", "Patch: old.patch", "%endif", "Patch: fix.patch",
			},
			prep: []string{"%autopatch -p1 -M10"}, number: -1, reason: "numbering is unknown",
		},
		{
			name:         "macro numbered target can use bulk application",
			declarations: []string{"Patch%{n}: fix.patch"}, prep: []string{"%autopatch -p1"},
		},
		{
			name:         "macro numbered target cannot satisfy numbered command",
			declarations: []string{"Patch%{n}: fix.patch"}, prep: []string{"%patch0 -p1"},
			number: -1, reason: "numbering is unknown",
		},
		{
			name: "conditional duplicate ignored",
			prep: []string{"%autopatch -p1", "%ifarch x86_64", "%patch0 -p1", "%endif"},
		},
		{
			name: "unsafe selection does not hide later valid selection",
			prep: []string{"%patch0 -p0", "%patch0 -p1"},
		},
		{
			name: "valid bulk selection does not hide earlier directory hazard",
			prep: []string{"cd lib", "%autopatch -p1"}, reason: "working directories",
		},
		{
			name: "unsupported prep prepend",
			prep: []string{"%prep -p", "%autosetup -p1"}, reason: "nonstandard",
		},
		{
			name: "shell directory name remains unresolved",
			prep: []string{"%autosetup -n $(unknown) -p1"}, reason: "shell",
		},
		{
			name: "unknown strip on excluded patch is irrelevant",
			prep: []string{"%patch1 -p%{strip}", "%patch0 -p1"},
		},
		{
			name: "pipeline before bulk application remains uncertain",
			prep: []string{"prepare | sh", "%autopatch -p1"}, reason: "shell control flow",
		},
		{
			name: "conditional source setup does not count as an unconditional setup",
			prep: []string{"%ifarch x86_64", "%setup -a1", "%endif", "%autosetup -p1"},
		},
		{
			name: "unresolved directory does not hide strip mismatch",
			prep: []string{"%autosetup -n %{name}-%{version} -p0"}, reason: "uses '-p0'",
		},
	}
	runPatchApplicationTests(t, tests)
}

func TestAnalyzePatchApplicationsPairedHeaders(t *testing.T) {
	tests := []patchApplicationTestCase{
		{
			name: "mail preamble is not a filename",
			prep: []string{"%patch0 -p1"},
			content: []string{
				"Subject: Fix behavior", "", "--- Previous behavior", "Explanation.",
				"+++ New behavior", "", "diff --git a/lib/file.c b/lib/file.c",
				"--- a/lib/file.c", "+++ b/lib/file.c", "@@ -1 +1 @@", "-old", "+new",
			},
		},
		{
			name: "preamble does not hide strip mismatch",
			prep: []string{"%patch0 -p2"},
			content: []string{
				"--- Previous behavior", "Explanation.", "--- a/file.c", "+++ b/file.c",
			},
			reason: "uses '-p2'",
		},
		{
			name:    "standalone deletion old path is checked",
			prep:    []string{"%patch0 -p2"},
			content: []string{"--- a/file.c", "+++ /dev/null", "@@ -1 +0,0 @@", "-old"},
			reason:  "uses '-p2'",
		},
		{
			name:    "standalone addition new path is checked",
			prep:    []string{"%patch0 -p2"},
			content: []string{"--- /dev/null", "+++ b/file.c", "@@ -0,0 +1 @@", "+new"},
			reason:  "uses '-p2'",
		},
	}
	runPatchApplicationTests(t, tests)
}

func TestAnalyzePatchApplicationsMissingSelectionMessage(t *testing.T) {
	for _, mode := range []spec.EditorMode{spec.EditorLegacy, spec.EditorStructural} {
		t.Run(string(mode), func(t *testing.T) {
			for _, command := range []string{
				"cp input output", "%autopatch -M%{limit} -p1", "%patch -P1 -p1",
				"%ifarch x86_64\n%patch0 -p1\n%endif", "for p in %patches; do\npatch -p1 < $p\ndone",
			} {
				t.Run(command, func(t *testing.T) {
					input := "Name: test\nPatch0: fix.patch\n%prep\n" + command + "\n"
					opened, err := spec.OpenSpec(strings.NewReader(input), spec.WithEditor(mode))
					require.NoError(t, err)
					diagnostics, err := opened.AnalyzePatchApplications([]spec.AddedPatch{{Filename: "fix.patch"}})
					require.NoError(t, err)
					require.Len(t, diagnostics, 1)
					assert.Empty(t, diagnostics[0].Command)
					assert.Equal(t,
						"no recognized command outside conditional branches selects this patch in '%prep'; "+
							"verify the preparation flow before adding an application command",
						diagnostics[0].Reason)
				})
			}
		})
	}
}

func TestAnalyzePatchApplicationsReviewRegressions(t *testing.T) {
	tests := []patchApplicationTestCase{
		{
			name: "negated exit prevents later application",
			prep: []string{"! exit 0", "%patch0 -p1"}, reason: "shell control flow",
		},
		{
			name: "negated directory change affects later application",
			prep: []string{"! cd lib", "%patch0 -p1"}, reason: "working directories",
		},
		{
			name: "negation before assignments and builtin prefixes",
			prep: []string{"! MODE=1 command -- exit 0", "%patch0 -p1"}, reason: "shell control flow",
		},
		{
			name: "negated ordinary command does not invalidate application",
			prep: []string{"! grep pattern file", "%patch0 -p1"},
		},
		{
			name: "negated lookup does not execute operand",
			prep: []string{"! command -v exit", "%patch0 -p1"},
		},
		{
			name: "later negated exit does not invalidate earlier selection",
			prep: []string{"%patch0 -p1", "! exit 0"},
		},
		{
			name: "assignment-prefixed exit prevents later application",
			prep: []string{"PATCH_MODE=1 exit 0", "%patch0 -p1"}, reason: "shell control flow",
		},
		{
			name: "assignment-prefixed cd changes patch directory",
			prep: []string{"CDPATH= cd lib", "%patch0 -p1"}, reason: "working directories",
		},
		{
			name: "command-prefixed cd changes patch directory",
			prep: []string{"command cd lib", "%patch0 -p1"}, reason: "working directories",
		},
		{
			name: "builtin-prefixed cd changes patch directory",
			prep: []string{"builtin cd lib", "%patch0 -p1"}, reason: "working directories",
		},
		{
			name: "assignments and nested execution prefixes",
			prep: []string{"A=1 B=2 command -p -- builtin cd lib", "%patch0 -p1"}, reason: "working directories",
		},
		{
			name: "command-prefixed exit is control flow",
			prep: []string{"command -- exit 0", "%patch0 -p1"}, reason: "shell control flow",
		},
		{
			name: "lookup-only command does not execute exit",
			prep: []string{"command -v exit", "%patch0 -p1"},
		},
		{
			name: "verbose lookup does not execute cd",
			prep: []string{"command -V cd", "%patch0 -p1"},
		},
		{
			name: "assignment-only line does not execute its value",
			prep: []string{"MODE=exit", "%patch0 -p1"},
		},
		{
			name: "ordinary assignment-prefixed command remains harmless",
			prep: []string{"MODE=1 echo cd lib", "%patch0 -p1"},
		},
		{
			name: "inline comment backslash does not continue shell",
			prep: []string{`echo preparing # comment \`, "%patch0 -p1"},
		},
		{
			name: "application comment backslash is not continuation",
			prep: []string{`%patch0 -p1 # comment \`},
		},
		{
			name: "escaped backslash pair is not continuation",
			prep: []string{`echo preparing \\`, "%patch0 -p1"},
		},
		{
			name: "odd backslash run continues shell",
			prep: []string{`echo preparing \\\`, "%patch0 -p1"}, reason: "continued",
		},
		{
			name: "backslash before space does not continue shell",
			prep: []string{"echo preparing \\ ", "%patch0 -p1"},
		},
		{
			name: "quoted backslash pair is not continuation",
			prep: []string{`echo '\\'`, "%patch0 -p1"},
		},
		{
			name: "expansion comment backslash does not continue shell",
			prep: []string{`%{echo:preparing} # comment \`, "%patch0 -p1"},
		},
		{
			name: "expansion escaped pair does not continue shell",
			prep: []string{`%{echo:preparing} \\`, "%patch0 -p1"},
		},
		{
			name: "multiline expansion comment backslash does not continue shell",
			prep: []string{"%{echo:", "preparing", `} # comment \`, "%patch0 -p1"},
		},
		{
			name: "multiline expansion escaped pair does not continue shell",
			prep: []string{"%{echo:", "preparing", `} \\`, "%patch0 -p1"},
		},
		{
			name: "closed expansion active backslash continues shell",
			prep: []string{`%{echo:preparing} \`, "%patch0 -p1"}, reason: "continued",
		},
		{
			name: "blank line ends shell continuation",
			prep: []string{"echo before \\", "", "%patch0 -p1"},
		},
		{
			name: "comment ends shell continuation",
			prep: []string{"echo before \\", "# comment", "%patch0 -p1"},
		},
		{
			name: "comment backslash does not continue shell command",
			prep: []string{"echo before \\", "# comment \\", "%patch0 -p1"},
		},
		{
			name:   "conditional blank does not alter unconditional continuation",
			prep:   []string{"echo before \\", "%ifarch x86_64", "", "# comment", "%endif", "%patch0 -p1"},
			reason: "continued",
		},
		{
			name: "attached subshell opener affects patch command",
			prep: []string{"(cd lib", "%patch0 -p1", ")"}, reason: "shell control flow",
		},
		{
			name: "quoted parentheses do not create subshell",
			prep: []string{`echo '(cd lib)'`, "%patch0 -p1"},
		},
		{
			name: "escaped parentheses do not create subshell",
			prep: []string{`echo \(cd lib\)`, "%patch0 -p1"},
		},
		{
			name: "context diff hunk ranges are not file paths",
			prep: []string{"%patch0 -p1"},
			content: []string{
				"*** a/file.c\t2026-01-01", "--- b/file.c\t2026-01-02",
				"***************", "*** 1,3 ****", "  first", "! old", "  last",
				"--- 1,3 ----", "  first", "! new", "  last",
			},
		},
		{
			name: "next context file header remains checked",
			prep: []string{"%patch0 -p1"},
			content: []string{
				"*** a/first.c", "--- b/first.c", "***************",
				"*** 1 ****", "! old", "--- 1 ----", "! new",
				"*** second.c", "--- second.c", "***************",
				"*** 1 ****", "! old", "--- 1 ----", "! new",
			},
			reason: "entire patch path",
		},
		{
			name: "unified file after context diff remains checked",
			prep: []string{"%patch0 -p1"},
			content: []string{
				"*** a/first.c", "--- b/first.c", "***************",
				"*** 1 ****", "! old", "--- 1 ----", "! new",
				"--- second.c", "+++ second.c", "@@ -1 +1 @@", "-old", "+new",
			},
			reason: "entire patch path",
		},
		{
			name: "context header strip mismatch still warns",
			prep: []string{"%patch0 -p2"},
			content: []string{
				"*** a/file.c", "--- b/file.c", "***************",
				"*** 1 ****", "! old", "--- 1 ----", "! new",
			},
			reason: "uses '-p2'",
		},
		{
			name:         "blank line ends continued definition before declaration",
			declarations: []string{"%define unused() echo unused \\", "", "Patch0: fix.patch"},
			prep:         []string{"%setup -q"}, reason: "no recognized command",
		},
		{
			name: "blank line ends continued definition before application",
			prep: []string{"%define unused() echo unused \\", "", "%patch0 -p1"},
		},
		{
			name: "comment line ends continued definition",
			prep: []string{"%define unused() echo unused \\", "# end definition", "%patch0 -p1"},
		},
		{
			name:   "comment line can continue macro body",
			prep:   []string{"%define unused() echo unused \\", "# continuation \\", "%patch0 -p1"},
			reason: "no recognized command",
		},
		{
			name:         "helper definition after description before sole prep",
			declarations: []string{"Patch0: fix.patch", "%description", "text", "%global __patch /bin/true"},
			prep:         []string{"%patch0 -p1"}, reason: "redefined",
		},
		{
			name:         "helper undefinition after description before sole prep",
			declarations: []string{"Patch0: fix.patch", "%description", "text", "%undefine autopatch"},
			prep:         []string{"%autopatch -p1"}, reason: "redefined",
		},
		{
			name: "conditional description definition ignored",
			declarations: []string{
				"Patch0: fix.patch", "%description", "%ifarch x86_64", "%undefine patch", "%endif",
			},
			prep: []string{"%patch0 -p1"},
		},
		{
			name: "later files helper definition does not invalidate application",
			prep: []string{"%patch0 -p1", "%files", "%global __patch /bin/true"},
		},
		{
			name: "spaced POSIX function is not executed",
			prep: []string{"apply_patches () {", "%patch0 -p1", "}"}, reason: "shell control flow",
		},
		{
			name: "function brace on following line",
			prep: []string{"apply_patches ()", "{", "%patch0 -p1", "}"}, reason: "shell control flow",
		},
		{
			name: "quoted parentheses are not function syntax",
			prep: []string{`echo "()"`, "%patch0 -p1"},
		},
		{
			name: "hunk lines resembling headers are not filenames",
			prep: []string{"%patch0 -p1"},
			content: []string{
				"--- a/file.c", "+++ b/file.c", "@@ -1 +1 @@", "--- old comment", "+++ new comment",
			},
		},
		{
			name: "multiple hunks and files preserve header checks",
			prep: []string{"%patch0 -p1"},
			content: []string{
				"--- a/first.c", "+++ b/first.c", "@@ -1 +1 @@", "--- old", "+++ new",
				"\\ No newline at end of file",
				"--- second.c", "+++ second.c", "@@ -1 +1 @@", "-old", "+new",
			},
			reason: "entire patch path",
		},
		{
			name: "hunk counts handle context and zero ranges",
			prep: []string{"%patch0 -p1"},
			content: []string{
				"--- a/first.c", "+++ b/first.c", "@@ -1,2 +1,2 @@", " context", "--- old", "+++ new",
				"@@ -5,0 +6,1 @@", "+++ inserted",
				"@@ -8,1 +8,0 @@", "--- removed",
				"--- a/second.c", "+++ b/second.c", "@@ -1 +1 @@", "-old", "+new",
			},
		},
	}
	runPatchApplicationTests(t, tests)
}

func TestAnalyzePatchApplicationsRemovedAndMissingFiles(t *testing.T) {
	for _, mode := range []spec.EditorMode{spec.EditorLegacy, spec.EditorStructural} {
		t.Run(string(mode), func(t *testing.T) {
			lines := []string{"Name: test", "Patch0: fix.patch", "%prep", "%autosetup -p1", ""}
			opened, err := spec.OpenSpec(strings.NewReader(strings.Join(lines, "\n")), spec.WithEditor(mode))
			require.NoError(t, err)

			patches := []spec.AddedPatch{{Filename: "fix.patch", FileMissing: true}}
			diagnostics, err := opened.AnalyzePatchApplications(patches)
			require.NoError(t, err)
			require.Len(t, diagnostics, 1)
			assert.Contains(t, diagnostics[0].Reason, "final file is missing")
			require.NoError(t, opened.RemovePatchEntry("fix.patch"))
			diagnostics, err = opened.AnalyzePatchApplications(patches)
			require.NoError(t, err)
			assert.Empty(t, diagnostics)
		})
	}
}

func TestPatchApplicationDiagnosticJSON(t *testing.T) {
	for _, number := range []int{0, -1, 2202} {
		diagnostic := spec.PatchApplicationDiagnostic{
			Filename: "fix.patch", PatchNumber: number, Reason: "uncertain",
		}
		data, err := json.Marshal(diagnostic)
		require.NoError(t, err)

		var decoded map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(data, &decoded))
		assert.Len(t, decoded, 3)
		assert.Equal(t, strconv.Itoa(number), string(decoded["patchNumber"]))

		diagnostic.Command = "%patch -p1"
		data, err = json.Marshal(diagnostic)
		require.NoError(t, err)
		assert.Contains(t, string(data), `"command":"%patch -p1"`)
	}
}
