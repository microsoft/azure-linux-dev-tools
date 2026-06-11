# RFC 001: Conditional-Aware Spec Editing

- **Status**: Draft
- **Author**: @trungams
- **Created**: 2026-05-22
- **Related issues**:
  - [#144](https://github.com/microsoft/azure-linux-dev-tools/issues/144) — `spec-remove-section` / `spec-remove-subpackage` produce invalid specs when sections are inside `%if/%endif`
  - [#193](https://github.com/microsoft/azure-linux-dev-tools/issues/193) — `spec-remove-subpackage` / `spec-remove-section` greedily consume `%if` guards of the following subpackage
  - [#203](https://github.com/microsoft/azure-linux-dev-tools/issues/203) — `spec-remove-subpackage` drops `%define` macros inside the removed subpackage that are referenced elsewhere
- **Related PRs**:
  - [#190](https://github.com/microsoft/azure-linux-dev-tools/pull/190) — `fix: balance conditional nesting when removing spec sections` (merged)

## Background

### Spec overlay system

Azure Linux imports RPM specs from upstream (primarily Fedora) and customizes them via an **overlay system** — no spec forking required. Overlays are declarative TOML directives that modify specs at render time. Key overlay types relevant to this RFC:

- **`spec-remove-section`** / **`spec-remove-subpackage`**: Remove entire sections or all sections belonging to a subpackage.
- **`spec-append-lines`**: Append lines at the end of a named section.
- **`spec-insert-tag`**: Insert a tag (e.g., `Source9999:`) after the last tag of the same family in a section.
- **`spec-search-replace`**: Regex-based search and replace within a section.

All of these operate on section ranges determined by `Spec.Visit`, a single-pass line walker that emits visitor targets (`SectionStartTarget`, `SectionLineTarget`, `SectionEndTarget`) as it walks the raw lines of a spec.

### The problem

`Spec.Visit` treats `%if/%endif` conditional directives as opaque content lines. It has no understanding of conditional structure. This leads to a family of bugs where operations produce incorrect results at section boundaries that involve conditionals.

The pattern is extremely common in Fedora specs: `%if` guards wrapping subpackages, `%define` macros inside conditional sections, and bcond-controlled feature blocks. As Azure Linux imports more complex packages, these issues surface increasingly.

### What has been fixed

PR [#190](https://github.com/microsoft/azure-linux-dev-tools/pull/190) added post-hoc conditional balancing (`balanceRange`) to `collectSectionRanges`, fixing section removal (issues #144 and #193). A separate `skipPastConditional` workaround already existed for `spec-insert-tag`. These are point fixes layered on top of Visit — they work but add complexity that is recognized as unsustainable. The PR review consensus was that with better test coverage, the conditional-handling logic should be unified and factored to prevent further accretion of ad-hoc workarounds.

### What remains broken

1. **`spec-append-lines` boundary bug**: `AppendLinesToSection` uses `SectionEndTarget` to find where to insert. When a `%if` sits between two sections (wrapping the next one), Visit considers it part of the current section, so appended lines land inside the conditional. `spec-insert-tag` has a workaround (`skipPastConditional`); `spec-append-lines` does not.

2. **`%define`/`%global` dropped during removal** (#203): `spec-remove-subpackage` deletes the entire block without checking whether macro definitions inside it are referenced from surviving sections. The macro silently vanishes, causing build failures downstream.

## Problem inventory

| # | Problem | Root cause | Severity |
|---|---------|------------|----------|
| 1 | `spec-append-lines` places lines inside straddling conditionals | Visit reports wrong section end | Incorrect output |
| 2 | `collectSectionRanges` needs post-hoc `balanceRange` | Visit reports wrong section end | Complexity / fragility |
| 3 | `spec-insert-tag` needs `skipPastConditional` workaround | Visit reports wrong section end | Complexity / fragility |
| 4 | `%define`/`%global` inside removed sections silently dropped | No semantic awareness of macro definitions | Incorrect output |
| 5 | `%else` branches with section headers create phantom sections | Visit walks both branches without conditional awareness | Potential incorrect operations |
| 6 | Continuation lines (`\`) misinterpreted as structural elements | Parser processes each physical line independently; no continuation state | Incorrect section tracking, false-positive errors |
| 7 | Section content spilling past wrapper `%endif` is invisible to section-scoped operations | Tree parser assigns section headers to wrapper blocks; content after `%endif` has no section context | Section-scoped overlays miss post-wrapper content |

Problems 1–3 share the same root cause: Visit treats `%if/%endif` as opaque content, so `SectionEndTarget` fires at the wrong position. Problem 4 requires understanding what's *inside* a section before deleting it. Problem 5 is a subtler structural issue: Visit sees sections in both `%if` and `%else` branches as real, coexisting sections, when at build time only one branch is active. Problem 6 is a parser-level issue: when a line ends with `\`, the next physical line is a continuation and should not be structurally interpreted. The parser currently ignores this, causing phantom section boundaries inside `%define`/`%global` bodies and false-positive `balanceRange` errors on `%if` continuation lines. Problem 7 is a fundamental tree-model limitation: when a section header is inside a `%if` wrapper but the section's content continues past `%endif`, the tree cannot associate the post-wrapper content with the section. This affects 1 spec in the corpus (gdb). The workaround is to use `spec-search-replace` instead of section-scoped overlays for tags in this position. See [overlays documentation](../../user/reference/config/overlays.md#section-scoped-operations-and-straddling-conditionals).

## RPM spec syntax survey

Beyond `%if/%endif` and `%define/%global`, a comprehensive solution must account for the full range of RPM spec syntax that could interact with structural analysis. This survey was conducted against the Azure Linux spec corpus (7,432 rendered specs as of May 2026).

### Actionable: patterns the structural model must handle

#### `%else`/`%elif` branches containing section headers (13 specs)

Some specs place different sections inside the `%if` and `%else` branches of a conditional:

```spec
%if %{with gui}
%description gui
GUI tools for the package.
%else
%description minimal
Minimal tools for the package.
%endif
```

Found in: kernel, gdb, firefox, java-openjdk, apr-util, dogtag-pki, linux-system-roles, and others.

Visit walks **both** branches and emits `SectionStartTarget`/`SectionEndTarget` for sections in each. This means section lists contain "phantom" sections that may not exist at build time. Implications:

- Section-targeted overlays could match a section that only exists in one branch.
- Removing a section inside one branch could orphan the other branch or break the conditional structure.
- For a tree model, `ConditionalWrapper` needs to carry sections in **both** `Then` and `Else` branches.

#### Line continuation (`\`) creates phantom structural elements (22+ specs)

When a line ends with `\`, RPM treats the next physical line as a continuation of the same logical line. Our parser processes each physical line independently and has no continuation awareness. This causes two classes of bugs:

**Phantom section boundaries in `%define`/`%global` bodies (22 specs, 68 phantom lines):**

```spec
%define kernel_gcov_package() \
%package %{?1:%{1}-}gcov\
Summary: gcov graph and source files for coverage data collection.\
%description %{?1:%{1}-}gcov\
%{?1:%{1}-}gcov includes the gcov graph and source files...\
%{nil}
```

The parser sees `%package` on line 2 as `tokens[0]`, matches `sectionTypesByName`, and emits a `SectionStartLine` — creating a phantom section boundary inside a macro definition. This corrupts Visit's section tracking. Corpus data: 68 phantom section lines and 148 phantom conditional lines across 22 specs (kernel, cross-binutils, cross-gcc, ccache, rust, python3.13, ghc, opencv, etc.).

**False-positive `balanceRange` errors on `%if` continuations (16 specs):**

```spec
%description pkgA
content

%if cond1 \
cond2
%package pkgB
...
%endif
```

When removing pkgA's section, `balanceRange` trims the range to exclude the straddling `%if`, then `validateNoContentAfter` checks the trimmed zone. The continuation line `cond2` has `conditionalDepthChange == 0` and `isBlankOrComment == false`, so it's wrongly flagged as "real content" — triggering a spurious `ErrConditionalSpansSections` error.

**Unified fix — `inContinuation` state flag:**

Both problems share the same root cause: the parser doesn't track continuation state. The fix is a single `inContinuation bool` in `parseState`. Any line ending with `\` sets it; the next physical line is forced to `RawLine` regardless of content (no section keyword or conditional directive detection); if that line also ends with `\`, the flag persists. This handles `%define` bodies, `%if` conditions, and any other continuation context uniformly.

```go
type parseState struct {
    currentSect    SectionTarget
    inContinuation bool
}
```

This is independent of the structural model work and could ship as a standalone PR. Physical lines remain unchanged — only structural interpretation is suppressed on continuation lines. Content operations (`spec-search-replace`) and serialization are unaffected.

#### `%define`/`%global` scoping and `%undefine` (pervasive)

RPM macros have dynamic scoping:
- `%define` defines a macro that persists until redefined or undefined.
- `%global` is the same but the body is expanded at definition time.
- `%undefine` removes the topmost definition from the macro stack.

For the macro hoisting feature (#203), `%undefine` must be considered: if a removed section contains both `%define foo` and later `%undefine foo`, hoisting only the `%define` would change semantics. The hoisting logic should treat `%define`/`%undefine` pairs as a unit.

### Documented limitations: patterns that require macro evaluation

The following patterns **cannot** be handled by static analysis. They are fundamental limitations of any approach that doesn't evaluate macros. These should be documented in user-facing overlay documentation so users understand when `spec-search-replace` is needed instead of structural overlays.

#### Macro-generated sections (529 specs — 7% of corpus)

Several Fedora packaging macros expand to full `%package`/`%description`/`%files` sections at build time:

| Macro | Spec count | Creates sections? |
|-------|-----------|-------------------|
| `%gometa` | 364 | No — only defines macros |
| `%ghc_lib_subpackage` | 194 | **Yes** — `%package`, `%description`, `%files` |
| `%pyproject_extras_subpkg` | 117 | **Yes** — `%package`, `%description` |
| `%fontpkg` | 22 | **Yes** — `%package`, `%description`, `%files` |

Our static parser sees these macro invocations as raw lines. The sections they generate are invisible. Overlays targeting sections created by these macros must use `spec-search-replace`.

#### `%{expand:...}` — deferred expansion (3,041 specs)

```spec
%global _description %{expand:
7-Zip is a file archiver with a high compression ratio.
}
```

Massively prevalent but used almost exclusively for multi-line string definitions (descriptions, changelogs), not for generating section headers. The parser sees the `%global` line, not the expanded content. No structural impact in practice.

#### `%{load:...}` — external macro files (88 specs)

```spec
%{load:%{_sourcedir}/automake.azl.macros}
```

Loads macro definitions from external files. In Azure Linux, these are primarily `azl.macros` files loaded via the `spec-insert-tag Source9999` overlay pattern. They define Azure Linux-specific macros, not section structure.

#### `%include` — file inclusion (15 specs)

```spec
%include %{SOURCE9998}
```

Found in gdb, efivar, efibootmgr, ansible. The included file could contain sections, conditionals, or macro definitions. Cannot be resolved without file access and macro evaluation.

### Non-issues: patterns already handled correctly

| Pattern | Prevalence | Why it's fine |
|---------|-----------|---------------|
| `%dnl` (comment directive) | 5 specs | Parser sees `%dnl` as first token → not in `sectionTypesByName` → raw line |
| `%bcond_with`/`%bcond_without` | Pervasive | Purely macro-level; structural impact is through `%if %{with ...}` which is already handled |
| `%{lua:...}` blocks | Pervasive (via `%autorelease`) | Inline macro expansion; parser sees the containing `%define` line |
| Comments (`#` lines) inside conditionals | Pervasive | Parser treats as empty lines; `conditionalDepthChange` ignores them |

### Summary

| Category | Prevalence | Structural impact | Solvable? |
|----------|-----------|-------------------|----------|
| `%if/%endif` straddling sections | Common | **High** — core problem | Yes |
| `%else` branches with section headers | 13 specs | **Medium** — phantom sections | Yes, needs branch-aware model |
| `%define/%global` in removed ranges | Occasional | **High** — silent breakage | Yes |
| `%undefine` interacting with hoisting | Rare | **Low** — edge case for #203 | Yes, hoist as unit |
| Line continuation (`\`) | 22+ specs | **High** — phantom sections + false-positive errors | Yes, `inContinuation` state flag in `parseState` |
| Macro-generated sections | 529 specs (7%) | **Medium** — invisible sections | **No** — document as limitation |
| `%{expand:}`, `%{load:}`, `%include` | Thousands | **Low** — text/macros, not structure | **No** — document as limitation |

## Research

### How other tools handle this

- **RPM itself** evaluates conditionals *before* parsing sections (two-phase: conditional preprocessing → section parsing). We cannot do this — we edit specs statically without evaluating macros.
- **[tree-sitter-rpmspec](https://gitlab.com/cryptomilk/tree-sitter-rpmspec)** faces the same fundamental problem we do, documented in their [DESIGN.md](https://gitlab.com/cryptomilk/tree-sitter-rpmspec/-/blob/main/rpmspec/DESIGN.md). They call it the "Section End Detection Problem" — sections have no explicit end markers, and conditionals can appear at different structural levels (wrapping sections vs inside sections). They describe the same chicken-and-egg problem: you need to know what's inside a `%if` body to determine how to parse the `%if` itself. Their solution is **context-aware lookahead** in the external scanner: before emitting a `%if` token, peek ahead to see if the body contains section keywords, and emit different token types (`top_level_if`, `scriptlet_if`, `subsection_if`, `files_if`). This is functionally equivalent to the two-pass approach proposed below (Pass 1: collect pairs + section positions → Pass 2: classify and build tree). Key differences: they build a full grammar parser and distinguish four conditional contexts (we need only two: wrapper vs block, since we don't validate section content); they share the same macro-evaluation limitation we do.
- **packit/specfile** (Python) uses a flat section list — section boundaries are determined by `%section_name` headers only. Lines have a per-line `valid` boolean for conditional state, but boundaries themselves are not conditional-aware.

### Existing Visit callers audit

All 9 `Visit` call sites are internal to the `spec` package (in `edit.go`). `VisitTarget` is only constructed inside `spec.go`. No external code creates `VisitTarget` values.

Three callers use `SectionEndTarget` and would be affected by boundary changes:

| Caller | Uses `SectionEndTarget` | Mutation | Impact of boundary change |
|--------|------------------------|----------|--------------------------|
| `findInsertTagPosition` | Records `sectionEndLineNum` | No | Boundary feeds into `skipPastConditional` |
| `AppendLinesToSection` | Inserts at `CurrentLineNum` | Yes (`InsertLinesBefore`) | Lines would move to correct position |
| `collectSectionRanges` | Records range end | No | Would simplify/eliminate `balanceRange` |

Six callers do NOT use `SectionEndTarget` and are unaffected:
`VisitTags`, `PrependLinesToSection`, `SearchAndReplace`, `AddChangelogEntry`, `HasSection`, `removePatchlistEntriesMatching`.

### Lock file independence

Lock file fields (`input-fingerprint`, `resolution-input-hash`) do **not** depend on rendered spec content. The rendered spec directory is explicitly excluded from fingerprinting (tagged `fingerprint:"-"`). Cosmetic changes to rendered specs have zero effect on lock files.

## Proposed approaches

### Option A: Enriched Visit context with pre-computed structure

This is an incremental approach that preserves the existing Visit API.

#### Core idea

Visit stays as a single-pass line walker. Before walking, a structural index is built from the raw lines and exposed on `Context` for visitors that need structural awareness.

#### What the structure looks like

```go
// Computed once per Visit call (or lazily on first access).
type SpecStructure struct {
    // Conditional pairs: every %if matched with its %endif.
    ConditionalPairs []conditionalPair

    // Section headers: line number + parsed section identity.
    SectionHeaders []sectionHeaderPos

    // Macro definitions: %define/%global with line number + name.
    MacroDefs []macroDef
}

// Derived: for a given section boundary (line of next section header),
// returns the "content end" adjusted for straddling conditionals.
func (ss *SpecStructure) ContentEndFor(sectionEnd int) int { ... }
```

Exposed on `Context`:

```go
type Context struct {
    // ... existing fields unchanged ...

    // Structure provides pre-computed structural info about the spec.
    Structure *SpecStructure  // new, additive
}
```

#### How each problem is solved

**Problem 1 — `AppendLinesToSection` boundary bug:**

```go
// Before (buggy):
ctx.InsertLinesBefore(lines)  // inserts at CurrentLineNum = next section header

// After:
contentEnd := ctx.Structure.ContentEndFor(ctx.CurrentLineNum)
ctx.spec.InsertLinesAt(lines, contentEnd)
```

**Problem 2 — `collectSectionRanges` post-hoc balancing:**

```go
// Before: collect raw ranges, then balanceRange each one.
// After: collect content-aware ranges directly.
case SectionEndTarget:
    if matched && curStart >= 0 {
        end := ctx.Structure.ContentEndFor(ctx.CurrentLineNum)
        ranges = append(ranges, sectionLineRange{start: curStart, end: end})
    }
```

`balanceRange` becomes either unnecessary or dramatically simpler (only the `%else` validation edge case survives).

**Problem 3 — `InsertTag` / `skipPastConditional`:**

```go
// Before: skipPastConditional scans from line 0 computing depth.
// After: look up in pre-computed ConditionalPairs directly.
insertAfterLine = ctx.Structure.SkipPastConditional(insertAfterLine, sectionEnd)
```

**Problem 4 — `%define` hoisting during removal:**

```go
// In removal logic, before deleting range [start, end):
defsInRange := structure.MacroDefsInRange(start, end)
for _, def := range defsInRange {
    if structure.IsReferencedOutsideRange(def.Name, start, end, rawLines) {
        s.InsertLinesAt([]string{rawLines[def.Line]}, hoistTarget)
    }
}
```

#### Mutation handling

Most mutating visitors access the structure **before** they mutate, then return immediately. Staleness is not an issue in practice. For safety, the structure can be marked dirty on any mutation and lazily recomputed on next access.

#### What gets simplified or removed

- `skipPastConditional` → replaced by `SpecStructure.ContentEndFor` or `SkipPastConditional`
- `collectConditionalPairs` call in `collectSectionRanges` → uses pre-computed pairs
- `balanceRange` → most logic absorbed into `ContentEndFor`; only `%else` validation survives

#### Incremental delivery

1. **PR A**: Add `SpecStructure` with `ConditionalPairs` + `ContentEndFor`. Fix `AppendLinesToSection` and `collectSectionRanges`. Simplify `balanceRange`.
2. **PR B**: Add `MacroDefs` + `IsReferencedOutsideRange`. Fix removal to hoist macros (#203).

#### Risks and limitations

- The structural model is **read-only metadata alongside mutable raw lines**. The two can drift after mutations. This is manageable because current callers don't interleave structure reads and line writes, but it's a latent hazard for future callers.
- Operations still ultimately manipulate line arrays by position. Complex transformations (move a section, restructure conditionals) remain fiddly — you compute positions from metadata and then do array surgery.
- Each new structural feature (macros, conditionals, future: `%autosetup` awareness?) needs explicit support in the pre-scan. The structure grows organically.

---

### Option B: Structural tree model (recommended)

This is a more ambitious approach that replaces the flat line walker with a parsed structure.

#### Core idea

Parse the spec into a tree where sections and conditionals are explicit nodes. Operations work on the tree. Serialization flattens back to lines.

#### What the tree looks like

```go
type SpecTree struct {
    Preamble *SectionNode
    Body     []BodyElement    // sections and top-level conditional wrappers
}

// BodyElement is either a SectionNode or a ConditionalWrapper.
type BodyElement interface {
    bodyElement()
    Lines() []string         // serialize back to text
}

type SectionNode struct {
    HeaderLine string        // e.g., "%build", "%package -n foo"
    Name       string
    Package    string
    Content    []ContentElement
}

// ContentElement is a text block, conditional block, or macro definition.
type ContentElement interface {
    contentElement()
    Lines() []string
}

type TextLines struct {
    lines []string
}

type ConditionalBlock struct {
    Directive string              // "%if %{with_docs}"
    Then      []ContentElement
    Else      []ContentElement    // nil if no %else
    Endif     string              // "%endif"
}

type MacroDef struct {
    Line  string    // "%define testsdir %{_libdir}/..."
    Name  string    // "testsdir"
}

// A conditional that wraps one or more entire sections.
type ConditionalWrapper struct {
    Directive string
    Body      []BodyElement       // sections inside the conditional
    Else      []BodyElement
    Endif     string
}
```

The key structural distinction: a `ConditionalBlock` lives **inside** a section (as content). A `ConditionalWrapper` lives **between** sections (wrapping them). The parser determines which based on whether the `%if`/`%endif` pair contains section headers.

#### Implementation strategy: start simple, add types later

For the initial implementation, a single recursive `Block` type is simpler to build and iterate on:

```go
type Block struct {
    Kind     BlockKind   // Section, Conditional, Text, MacroDef
    Header   string      // "%build", "%if %{with_docs}", "%define foo ..."
    Name     string      // section name / macro name
    Package  string      // for section blocks
    Lines    []string    // raw text lines (leaf Text blocks)
    Children []*Block    // nested blocks
    Else     []*Block    // for Conditional blocks with %else
}
```

This trades type safety for simplicity — the type system won't prevent invalid nesting (e.g., a section inside a section), but the parser controls construction so this is safe in practice. Once the parser and operations are proven, the model can be promoted to the typed interface hierarchy above, making illegal states unrepresentable.

#### Parsing

The parser is two-pass:

1. **Pass 1**: Collect conditional pairs and section header positions (same pre-scan as the enriched metadata approach).
2. **Pass 2**: Build the tree — for each conditional pair, determine whether it's a wrapper (spans section boundaries) or content (fully within a section). Straddling cases (a `%if` that starts partway through a section and closes in the next) are errors — same as today.

Example input and resulting tree:

```
Input:                           Tree:

%if %{with_docs}                 ConditionalWrapper
%package docs                      ├─ Then:
Summary: Docs                      │   ├─ SectionNode(%package docs)
%description docs                  │   │    └─ TextLines["Summary: Docs"]
Documentation.                     │   └─ SectionNode(%description docs)
%endif                             │        └─ TextLines["Documentation."]

%build                           SectionNode(%build)
make                               ├─ TextLines["make"]
%if %{with_docs}                   └─ ConditionalBlock
make docs                               └─ Then: TextLines["make docs"]
%endif

%install                         SectionNode(%install)
make install                       └─ TextLines["make install"]
```

Example with `%else` branches containing different sections:

```
Input:                           Tree:

%if %{with gui}                  ConditionalWrapper
%description gui                   ├─ Then:
GUI tools.                         │   └─ SectionNode(%description gui)
%else                              │        └─ TextLines["GUI tools."]
%description minimal               └─ Else:
Minimal tools.                         └─ SectionNode(%description minimal)
%endif                                      └─ TextLines["Minimal tools."]
```

This case requires `ConditionalWrapper.Else` to hold `[]BodyElement`, not just `[]ContentElement`. Removing a section inside one branch must not disturb the other branch.

#### How each problem is solved

**Problem 1 — `AppendLinesToSection`:**

```go
func (s *SpecTree) AppendToSection(name, pkg string, lines []string) error {
    section := s.FindSection(name, pkg)
    section.Content = append(section.Content, &TextLines{lines: lines})
    return nil
}
```

No boundary confusion possible — conditionals are separate nodes, not inline content.

**Problem 2 — Section removal:**

```go
func (s *SpecTree) RemoveSection(name, pkg string) {
    s.Body = slices.DeleteFunc(s.Body, func(e BodyElement) bool {
        sec, ok := e.(*SectionNode)
        return ok && sec.Name == name && sec.Package == pkg
    })
    // Also handle sections inside ConditionalWrappers
}
```

The conditional wrapper stays in place. No `balanceRange` needed. Empty wrappers can be collapsed in a cleanup pass.

**Problem 3 — `InsertTag`:**

Tags are content elements within a section. Conditional blocks are sibling content elements. Inserting after a tag naturally avoids landing inside a conditional — no `skipPastConditional` needed.

**Problem 4 — `%define` hoisting:**

```go
func (s *SpecTree) RemoveSectionWithMacroHoist(name, pkg string) {
    section := s.FindSection(name, pkg)
    for _, elem := range section.Content {
        if def, ok := elem.(*MacroDef); ok {
            if s.IsNameReferencedElsewhere(def.Name, section) {
                s.Preamble.Content = append(s.Preamble.Content, def)
            }
        }
    }
    s.RemoveSection(name, pkg)
}
```

#### Visit compatibility layer

Visit can be reimplemented as a tree walk emitting the same targets, or kept as-is on raw lines during a migration period. Alternatively, a new tree-based API (`SpecTree.Walk(...)`) can coexist with Visit, and callers migrate gradually.

#### Incremental delivery

1. **PR A**: Add `SpecTree` construction from raw lines + serialization back to lines. Round-trip test: `parse → serialize = identity`.
2. **PR B**: Implement tree-based `AppendToSection` and `RemoveSection`. Wire up overlay dispatch to use tree operations for `spec-append-lines` and `spec-remove-section/subpackage`.
3. **PR C**: Implement tree-based `InsertTag`, `SearchAndReplace`. Migrate more overlays.
4. **PR D**: Macro hoisting (#203) using tree traversal.
5. **PR E** (optional): Reimplement Visit as a tree walk, or deprecate in favor of tree API.

#### Risks and limitations

- **Parsing complexity**: Building the tree requires handling ambiguous cases. `%if` / `%else` where each branch contains different sections is valid (13 specs in Azure Linux) and makes the tree shape depend on conditional structure. The parser must distinguish three conditional geometries: fully inside a section (→ `ConditionalBlock`), wrapping sections (→ `ConditionalWrapper`), and straddling a section boundary (→ error).
- **`%else` branch asymmetry**: The `Then` and `Else` branches of a `ConditionalWrapper` can contain different numbers and types of sections. Operations like `FindSection` must search both branches, and removal must handle the case where a section exists only in one branch.
- **Serialization fidelity**: Round-tripping must preserve the original text exactly (whitespace, comments, blank lines). Achievable but needs extensive testing against real specs.
- **Migration cost**: 9 Visit call sites + external callers need migration or a compatibility layer. This is the largest concrete risk.
- **Scope**: Multi-PR effort. Each PR needs testing against real-world specs (qemu, kernel, etc.).
- **Macro-generated sections are invisible**: 529 specs (7%) use macros like `%fontpkg`, `%ghc_lib_subpackage`, or `%pyproject_extras_subpkg` that expand to sections at build time. The tree model cannot represent these — they appear as raw text nodes. This is a fundamental limitation shared with the enriched metadata approach and must be documented.

## Comparison

| Dimension | Enriched metadata (Option A) | Structural tree (Option B) |
|-----------|------------------------------|----------------------------|
| **Core model** | Flat lines + structural metadata | Tree of sections / conditionals / macros |
| **Visit API** | Unchanged (additive field) | Compatibility layer or new API |
| **Boundary problem** | Metadata provides correct positions | Structurally impossible to get wrong |
| **Macro hoisting** | Scan metadata + line surgery | Move tree nodes between parents |
| **Mutation model** | Positional line array edits | Tree node manipulation |
| **New operations** | Each needs position computation from metadata | Natural tree traversal |
| **Migration effort** | 3 callers change (use `ContentEnd`) | All callers eventually migrate |
| **First PR size** | Small | Medium (tree parser + serializer + round-trip tests) |
| **Risk** | Low — additive, reversible | Moderate — new parser, migration period |
| **Long-term payoff** | Moderate — still doing line surgery | High — structural operations become trivial |
| **Ceiling** | Hits limits when operations need to restructure conditionals | No structural ceiling |

## Macro hoisting algorithm (issue #203)

When `spec-remove-section` / `spec-remove-subpackage` deletes a block, any `%define`/`%global` inside it is deleted too. If a *surviving* section still references that macro, the build breaks with a dangling `%{...}`. The tree parser (PR #215) added a first cut at hoisting: for each macro found inside a removed section, if its name is referenced outside the removed subtrees, splice the `%define` line in just before the first removed top-level block.

That first cut is correct only for the simplest case (a single, self-contained macro). PR review surfaced a family of edge cases the algorithm must handle before it can be trusted. This section specifies the target algorithm.

### Worked example: the transitive-dependency bug

```spec
%package tests
%define testroot %{_libdir}/foo
%define testsdir %{testroot}/tests

%files tests
%{testsdir}/*

%install
mkdir -p %{buildroot}%{testsdir}
```

Removing `%package tests` (and its `%files`/`%description`) must keep `%install` working. The current logic hoists `testsdir` (referenced by `%install`) but **not** `testroot` — `testroot`'s only reference lives in `%define testsdir`, which is itself inside the removed subtree, so the "referenced outside" check skips it. Result: a hoisted `%define testsdir %{testroot}/tests` with `testroot` undefined. The exact silent breakage #203 set out to fix, displaced one link down the chain.

### Edge cases the algorithm must handle

| # | Edge case | Failure if ignored | Handling |
|---|-----------|--------------------|----------|
| 1 | **Transitive dependency** — hoisted macro references another removed macro | Dangling `%{inner}` reference | Compute transitive closure: when hoisting `X`, also hoist any removed macro `X`'s body references, repeating to a fixed point |
| 2 | **Dependency cycle / self-reference** — `%define a %{b}` / `%define b %{a}`, or `%define a %{a}_x` | Infinite loop in closure walk | Track a `visited` set keyed by macro name; never enqueue a name twice |
| 3 | **Declaration order after closure** — dependency hoisted after its dependent, critical for `%global` (eager expansion) | `%global` expands against an undefined macro at definition time | Emit the closure set in original declaration order (the order `collectMacrosInSections` yields), not discovery order |
| 4 | **Hoist target too early in file** — a survivor *above* the removed block references the macro | `%define` lands after its first use; undefined at parse time | Hoist to the **end of the preamble** (after the last preamble tag, before the first real section), not "just before the first removed block" |
| 5 | **Duplicate / shadowing definitions** — same name in two removed subpackages, or name already defined in the preamble | Duplicate `%define` at root, or a subpackage value clobbers the preamble value for all survivors | Dedup hoisted names; do **not** hoist a name that already has a surviving definition (the survivor reference resolves to the existing one) |
| 6 | **`%undefine` in removed scope** — `%define foo` paired with a later `%undefine foo` inside the same removed block | Hoisting makes `foo` globally defined when it was meant to be scoped/torn down | Treat `%define`+`%undefine` as a unit: do not hoist a macro that is `%undefine`d within the removed set |
| 7 | **Bare-form reference over-match** — `macroReferencePattern`'s bare `%name\b` arm matches `%test` inside `%test-suite` | Unnecessary (usually harmless) hoist; combined with #5 could clobber | Tighten the bare arm, or accept over-hoisting as safe except where it collides with an existing definition (#5 guard covers the dangerous case) |

`%undefine` (#6) is currently invisible to the parser — `isMacroDefLine` recognizes only `%define`/`%global`, so `%undefine` parses as plain text. Handling #6 requires detecting `%undefine` lines within the removed set.

### Target algorithm

```
hoistReferencedMacros(root, removedSections):
    removedSet   ← identity set of all removed section blocks
    removedMacros ← collectMacrosInSections(removedSections)   # declaration order
    undefined    ← names %undefine'd within removedSet          # edge case 6
    survivingDefs ← macro names with a definition outside removedSet  # edge case 5

    # Seed: macros directly referenced by surviving content.
    toHoist ← ordered set
    visited ← {}
    queue   ← [ m in removedMacros if isReferencedOutside(root, m.Name, removedSet) ]

    # Fixed-point closure over intra-removed dependencies (edge cases 1, 2).
    while queue not empty:
        m ← queue.pop()
        if m.Name in visited: continue
        visited.add(m.Name)

        if m.Name in undefined:        continue   # edge case 6
        if m.Name in survivingDefs:    continue   # edge case 5 (already defined)
        toHoist.add(m)

        for ref in macroNamesReferencedBy(m.Body):
            if ref is the name of some macro in removedMacros and ref not in visited:
                queue.push(that macro)

    if toHoist empty: return

    dedup toHoist by name, keep first declaration                 # edge case 5
    order toHoist by original declaration order                   # edge case 3
    insert toHoist at end-of-preamble                             # edge case 4
```

Key properties:

- **Closure, not single-pass** — fixes the transitive bug; the `visited` set makes it terminate on cycles.
- **Declaration-order emission** — safe for both lazy `%define` and eager `%global`.
- **Preamble-end target** — correct regardless of where the surviving reference sits in the file.
- **Conservative on conflicts** — never overwrites a definition a survivor already sees; never resurrects a macro the author explicitly `%undefine`d in-scope.

### Out of scope (documented limitations)

- **Macros defined via macro expansion** (`%{expand:%define ...}`, `%{lua:...}`) are invisible to the static scan — same fundamental limitation as macro-generated sections.
- **Conditional definitions** — a macro defined only inside one branch of a `%if` within the removed set is hoisted unconditionally if referenced; we do not reconstruct the guarding condition. Acceptable because the alternative (no hoist) is strictly worse, but worth noting.

## Open questions

1. Which approach (enriched metadata vs structural tree) is the right level of ambition given the current and projected demand for spec editing features?
2. For the enriched metadata approach: should `SpecStructure` live on `Context` (per-visit, transient) or on `Spec` (persistent, invalidated on mutation)?
3. For the structural tree: is `ConditionalWrapper` vs `ConditionalBlock` the right split, or should all conditionals be one type with a "wraps sections" flag?
4. Should we prototype against a complex real-world spec (e.g., qemu) early to validate the approach?
5. Is there an intermediate path — e.g., start with enriched metadata for quick wins, then migrate to the structural tree as the model matures?
6. How should operations handle sections inside `%else` branches? Should `FindSection` return all matches (both branches), or require the caller to specify which branch?
7. Should macro-generated sections (from `%fontpkg`, `%ghc_lib_subpackage`, etc.) be documented as an explicit limitation in the overlay user guide, with guidance to use `spec-search-replace` for those cases?
8. Should the `inContinuation` fix (problem 6) ship as a standalone PR before the structural model work, since it's independent and low-risk?
9. Should macro hoisting (#203) be on-by-default or opt-in? "No magic" argues for an explicit `hoist-macros = [...]` list; ergonomics argue for on-by-default (the leak is usually discovered only via a build failure). Proposed compromise: on-by-default with a log line per hoist, plus an optional override list.
10. Should section/branch selection expose a mode enum (`First` / `All` / `SpecificN` / `RequireUnique`) for read and write operations, and should selection be by document order or by branch condition? (Condition-based selection requires macro evaluation we don't have.)
