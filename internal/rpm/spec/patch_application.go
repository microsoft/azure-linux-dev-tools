// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/kballard/go-shellquote"
)

// AddedPatch is an overlay-added patch, with its final contents.
type AddedPatch struct {
	Filename    string
	Content     []byte
	FileMissing bool
}

// PatchApplicationDiagnostic describes a problem with an overlay-added patch.
// [PatchApplicationDiagnostic.PatchNumber] is -1 when numbering is unknown.
type PatchApplicationDiagnostic struct {
	Filename    string `json:"filename"`
	PatchNumber int    `json:"patchNumber"`
	Command     string `json:"command,omitempty"`
	Reason      string `json:"reason"`
}

const (
	prepCommand           = "%prep"
	setupCommand          = "%setup"
	patchCommand          = "%patch"
	autosetupCommand      = "%autosetup"
	autopatchCommand      = "%autopatch"
	forgeautosetupCommand = "%forgeautosetup"
	goprepCommand         = "%goprep"
	patchlistCommand      = "%patchlist"
	patchBackend          = "patch"
	gitBackend            = "git"
)

type patchDeclaration struct {
	filename      string
	number        int
	conditional   bool
	numberUnknown bool
}

type patchConsumer struct {
	command            string
	macro              string
	numbers            []int
	minimum            int
	maximum            int
	strip              int
	backend            string
	disabled           bool
	unresolved         string
	selectionUncertain bool
	removesVendor      bool
}

// Only unconditional application is analyzed. Execution uncertainty is captured
// on each consumer so later commands cannot invalidate an earlier selection.
type patchApplicationScanner struct {
	// declarations holds patch tags and patchlist entries in document order.
	declarations []patchDeclaration
	// consumers holds unconditional setup/application commands with their captured uncertainty.
	consumers []patchConsumer
	// section is the current section keyword; empty denotes the main preamble.
	section string
	// depth tracks RPM conditional nesting; negative values indicate unmatched closing directives.
	depth int
	// nextNumber is one above the highest assigned number for a bare Patch tag or patchlist entry.
	nextNumber int
	// nextNumberUncertain marks automatic numbering affected by conditional or opaque declarations.
	nextNumberUncertain bool
	// prepCount counts encountered prep headers to detect missing or repeated preparation sections.
	prepCount int
	// execution remembers shell/directory hazards affecting current and subsequent consumers.
	execution string
	// prep records structural or hook uncertainty that prevents accepting any preparation candidate.
	prep string
	// backend is the inherited application backend for subsequent setup and bulk commands.
	backend string
	// redefined tracks unconditional local macro changes seen before the current invocation.
	redefined map[string]bool
	// macro tracks delimiters and language state while consuming a macro body or expansion.
	macro macroState
	// macroBody indicates that a definition or expansion continues onto another physical line.
	macroBody bool
	// expansion distinguishes an expansion being collected from a definition whose body is skipped.
	expansion bool
	// expansionText buffers expansion lines until the complete expression can be inspected.
	expansionText strings.Builder
	// continuation remembers a trailing shell backslash for detecting continued application commands.
	continuation bool
}

// AnalyzePatchApplications inspects only added patches still declared in the
// final spec. Both editors share this lexical visitor. It does not evaluate
// conditional branches, detect duplicate application, evaluate macros, or inspect
// loaded macro files; standard RPM macro behavior is assumed.
func (s *Spec) AnalyzePatchApplications(patches []AddedPatch) ([]PatchApplicationDiagnostic, error) {
	scanner := patchApplicationScanner{backend: patchBackend, redefined: make(map[string]bool)}

	if err := s.Visit(func(ctx *Context) error {
		if ctx.RawLine != nil {
			scanner.scanLine(*ctx.RawLine)
		}

		return nil
	}); err != nil {
		return nil, fmt.Errorf("reading spec for patch application analysis:\n%w", err)
	}

	if scanner.depth != 0 || scanner.macroBody {
		scanner.prep = "unbalanced conditional structure or unterminated macro body makes application uncertain"
	}

	var diagnostics []PatchApplicationDiagnostic

	for _, patch := range patches {
		if diagnostic := scanner.check(patch); diagnostic != nil {
			diagnostics = append(diagnostics, *diagnostic)
		}
	}

	return diagnostics, nil
}

// scanLine routes a physical line to macro, conditional, section, declaration,
// or preparation handling without trusting the editor's section classification.
func (s *patchApplicationScanner) scanLine(raw string) {
	line := strings.TrimSpace(raw)
	if s.scanMacro(raw, line) {
		return
	}

	if line == "" || strings.HasPrefix(line, "#") {
		if s.section == prepCommand && s.depth == 0 {
			s.continuation = false
		}

		return
	}

	change := conditionalDepthChange(line)
	if change != 0 || isConditionalBranchDirective(line) {
		s.depth += change
		if s.depth < 0 {
			s.prep = "unbalanced conditional structure makes application uncertain"
		}

		return
	}

	token := strings.Fields(line)[0]
	if _, known := sectionTypesByName[token]; known {
		s.startSection(line, token)

		return
	}

	switch {
	case isTagBearingSection(s.section):
		s.scanDeclaration(line)
	case s.section == patchlistCommand:
		s.addDeclaration(line, s.nextNumber, true)
	case s.section == prepCommand:
		s.scanPrep(raw, line, token)
	}
}

// startSection updates the section and resets command continuation, marking
// repeated, conditional, or augmented preparation sections as unsupported.
func (s *patchApplicationScanner) startSection(line, token string) {
	if token == prepCommand {
		s.prepCount++
		if s.prepCount != 1 || s.depth != 0 || line != prepCommand {
			s.prep = "multiple, conditional, or nonstandard preparation sections cannot be resolved"
		}
	}

	s.section = token
	s.continuation = false
}

// scanMacro consumes definitions and expansions, keeping multiline macro bodies
// out of ordinary command parsing. It reports whether the line was consumed.
func (s *patchApplicationScanner) scanMacro(raw, line string) bool {
	if s.macroBody {
		s.macro, s.macroBody = macroBodyStateAfter(raw, s.macro)
		if s.expansion {
			s.collectExpansion(raw)
		}

		return true
	}

	name, definition := isMacroDefLine(line)

	fields := strings.Fields(line)
	if len(fields) >= 2 && fields[0] == "%undefine" {
		s.noteDefinition(fields[1])

		return true
	}

	state := macroStateAfter(raw, macroState{})
	if !definition && !strings.HasPrefix(line, "%{") && !state.open() {
		return false
	}

	s.expansion = !definition

	s.macro, s.macroBody = macroBodyStateAfter(raw, macroState{})
	if definition {
		s.noteDefinition(name)
	} else {
		s.collectExpansion(raw)
	}

	return true
}

// collectExpansion buffers an expansion until its delimiters close, then inspects
// it. A trailing shell backslash affects continuation, not the macro boundary.
func (s *patchApplicationScanner) collectExpansion(raw string) {
	s.macroBody = s.macro.open()
	s.expansionText.WriteString(raw)
	s.expansionText.WriteByte('\n')

	if !s.macroBody {
		s.noteExpansion(s.expansionText.String())
		s.expansionText.Reset()
	}

	s.scanContinuation(raw)
}

// noteDefinition records unconditional local macro changes that may affect later
// consumers; preparation hooks make the overall preparation flow unsupported.
func (s *patchApplicationScanner) noteDefinition(name string) {
	if s.depth != 0 {
		return
	}

	s.redefined["%"+name] = true
	if strings.HasPrefix(name, "__spec_prep_") {
		s.prep = "custom preparation hooks cannot be resolved"
	}
}

// noteExpansion records automatic-numbering uncertainty and visible preparation
// shell context without evaluating expansions or inspecting loaded contents.
func (s *patchApplicationScanner) noteExpansion(line string) {
	if s.depth != 0 {
		return
	}

	if s.section == prepCommand {
		s.noteExecutionContext(withoutPatchExpansions(line, "%{"))
	}

	// Loaded file contents are deliberately outside this lexical analysis.
	// Other expansions are opaque, including conditional macro definitions.
	line = withoutPatchExpansions(line, "%{load:")
	if line == "" {
		return
	}

	if (isTagBearingSection(s.section) || s.section == patchlistCommand) &&
		strings.Contains(strings.ToLower(line), "patch") {
		s.nextNumberUncertain = true
	}
}

// withoutPatchExpansions removes braced expansions matching opener, including
// nested contents, and returns the surrounding text without evaluating it.
func withoutPatchExpansions(line, opener string) string {
	var visible strings.Builder

	for {
		start := strings.Index(line, opener)
		if start < 0 {
			visible.WriteString(line)

			return strings.TrimSpace(visible.String())
		}

		visible.WriteString(line[:start])

		state := macroState{}

		end := start
		for end < len(line) {
			closing := strings.IndexByte(line[end:], '}')
			if closing < 0 {
				end = len(line)

				break
			}

			next := end + closing + 1
			state = macroStateAfter(line[end:next], state)
			end = next

			if !state.open() {
				break
			}
		}

		line = line[end:]
	}
}

// scanDeclaration reads a patch tag's filename and explicit or automatic number,
// retaining the filename even when a macro-based tag number cannot be resolved.
func (s *patchApplicationScanner) scanDeclaration(line string) {
	tag, value, ok := parseTagLine(line)
	if !ok || !strings.HasPrefix(strings.ToLower(tag), "patch") {
		return
	}

	number, numbered := ParsePatchTagNumber(tag)
	switch {
	case strings.EqualFold(tag, "patch"):
		number = s.nextNumber
	case !numbered:
		s.declarations = append(s.declarations, patchDeclaration{
			filename: value, number: -1, conditional: s.depth != 0, numberUnknown: true,
		})
		s.nextNumberUncertain = true

		return
	}

	s.addDeclaration(value, number, strings.EqualFold(tag, "patch"))
}

// addDeclaration stores a patch entry and advances automatic numbering, carrying
// uncertainty from conditional entries into subsequent implicit numbers.
func (s *patchApplicationScanner) addDeclaration(filename string, number int, implicit bool) {
	declaration := patchDeclaration{
		filename: filename, number: number, conditional: s.depth != 0,
		numberUnknown: implicit && s.nextNumberUncertain,
	}
	s.declarations = append(s.declarations, declaration)
	s.nextNumber = max(s.nextNumber, number+1)
	s.nextNumberUncertain = s.nextNumberUncertain || declaration.conditional
}

// scanPrep skips conditional commands and collects unconditional consumers with
// their current backend, local redefinitions, continuation, and execution hazards.
func (s *patchApplicationScanner) scanPrep(raw, line, token string) {
	if s.depth != 0 {
		return
	}

	s.noteExecutionContext(raw)

	continued := s.scanContinuation(raw)

	if token != setupCommand && !isPatchConsumer(token) {
		return
	}

	consumer := parsePatchConsumer(line, s.backend)
	if continued {
		consumer.unresolved = "continued patch application commands cannot be resolved"
	}

	if token == setupCommand || isPatchSetup(token) {
		s.backend = consumer.backend
	}

	if s.consumerRedefined(consumer) {
		s.execution = "locally redefined patch application macro makes subsequent patch application uncertain"
	}

	if s.execution != "" {
		consumer.unresolved = s.execution
	}

	s.consumers = append(s.consumers, consumer)
}

// scanContinuation updates unconditional shell continuation from lexical escape
// state and reports whether this line continues from or into another line.
func (s *patchApplicationScanner) scanContinuation(raw string) bool {
	previous := s.continuation
	if s.depth == 0 {
		_, _, s.continuation = patchShellContext(raw)
	}

	return previous || s.continuation
}

// noteExecutionContext remembers directory changes and shell control flow that
// make the current and subsequent consumers uncertain, without executing shell.
func (s *patchApplicationScanner) noteExecutionContext(line string) {
	visible, operators, continuation := patchShellContext(line)

	// Physical-line continuations are tracked separately on the actual command.
	if continuation {
		visible = strings.TrimSuffix(visible, "\\")
	}

	fields, err := shellquote.Split(visible)
	if operators || err != nil {
		s.execution = "shell control flow or here-document makes patch application uncertain"

		return
	}

	if len(fields) == 0 {
		return
	}

	switch patchShellCommand(fields) {
	case "cd", "pushd", "popd":
		s.execution = "patch application uses multiple working directories; the target directory cannot be verified"
	case "if", "for", "while", "until", "case", "then", "else", "elif", "fi", "done", "esac",
		"function", "{", "(", "exit", "return", "exec", "break", "continue":
		s.execution = "shell control flow makes patch application uncertain"
	}
}

// patchShellCommand finds the executed command after negation, assignments, and
// command/builtin prefixes. Lookup-only forms do not execute their operands.
func patchShellCommand(fields []string) string {
	if len(fields) != 0 && fields[0] == "!" {
		fields = fields[1:]
	}

	for len(fields) != 0 {
		if patchShellAssignment(fields[0]) {
			fields = fields[1:]

			continue
		}

		if fields[0] != "command" && fields[0] != "builtin" {
			return fields[0]
		}

		fields = fields[1:]
		for len(fields) != 0 && strings.HasPrefix(fields[0], "-") {
			option := fields[0]
			fields = fields[1:]

			if option == "--" {
				break
			}

			if option != "-p" {
				return ""
			}
		}
	}

	return ""
}

// patchShellAssignment recognizes portable shell variable-assignment prefixes,
// without treating arguments containing '=' as commands to execute.
func patchShellAssignment(word string) bool {
	name, _, assignment := strings.Cut(word, "=")

	return assignment && name != "" && (name[0] < '0' || name[0] > '9') &&
		strings.Trim(name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_") == ""
}

// patchShellContext only distinguishes literal shell operators from quoted or
// commented text and active trailing escapes. It does not execute commands.
func patchShellContext(line string) (visible string, operators, continuation bool) {
	var quote rune

	escaped, wordStart := false, true

	for index, char := range line {
		switch {
		case escaped:
			escaped = false
			wordStart = false
		case quote == '\'':
			if char == '\'' {
				quote = 0
			}
		case char == '\\':
			escaped = true
		case quote == '"':
			if char == '"' {
				quote = 0
			}
		case strings.ContainsRune("'\"", char):
			quote = char
			wordStart = false
		case char == '#' && wordStart:
			return line[:index], operators, false
		case strings.ContainsRune(";&|()", char):
			operators = true
			wordStart = true
		case char == '<' && strings.HasPrefix(line[index:], "<<"):
			operators = true
		default:
			wordStart = strings.ContainsRune(" \t\n", char)
		}
	}

	return line, operators, escaped
}

// isPatchSetup identifies supported wrappers that combine source setup with
// optional patch application, rather than plain source extraction.
func isPatchSetup(token string) bool {
	return token == autosetupCommand || token == forgeautosetupCommand || token == goprepCommand
}

// isPatchConsumer recognizes supported application macros, including legacy
// '%patchN' forms with a literal non-negative number.
func isPatchConsumer(token string) bool {
	if isPatchSetup(token) || token == autopatchCommand || token == patchCommand {
		return true
	}

	if !strings.HasPrefix(token, patchCommand) {
		return false
	}

	number, err := strconv.Atoi(strings.TrimPrefix(token, patchCommand))

	return err == nil && number >= 0
}

// setupRedefined checks invoked source-setup dependencies even when patch
// application is disabled, since their shell effects can reach later commands.
func (s *patchApplicationScanner) setupRedefined(consumer patchConsumer) bool {
	if consumer.macro != setupCommand && !isPatchSetup(consumer.macro) {
		return false
	}

	dependencies := []string{consumer.macro}

	if isPatchSetup(consumer.macro) {
		dependencies = append(dependencies, setupCommand, autosetupCommand, "%__scm", "%__scm_setup_"+consumer.backend)
		if consumer.backend == gitBackend {
			dependencies = append(dependencies, "%__git")
		}
	}

	if consumer.macro == goprepCommand {
		dependencies = append(dependencies, "%gomkdir")
	}

	return slices.ContainsFunc(dependencies, func(name string) bool { return s.redefined[name] })
}

// consumerRedefined checks invoked setup/application dependencies for local
// changes whose shell effects can reach both this and subsequent consumers.
func (s *patchApplicationScanner) consumerRedefined(consumer patchConsumer) bool {
	if s.setupRedefined(consumer) {
		return true
	}

	dependencies := []string{consumer.macro}

	if strings.HasPrefix(consumer.macro, patchCommand) && consumer.macro != patchCommand {
		dependencies = append(dependencies, patchCommand)
	}

	applies := !consumer.disabled

	if isPatchSetup(consumer.macro) || consumer.macro == autopatchCommand {
		if !consumer.disabled {
			dependencies = append(dependencies, autopatchCommand)
		}

		// Bulk helpers are not invoked when no declaration can enter the selection.
		applies = applies && slices.ContainsFunc(s.declarations, func(declaration patchDeclaration) bool {
			return declaration.numberUnknown || consumer.selectionUncertain || consumer.selects(declaration.number)
		})
		if applies {
			dependencies = append(dependencies, "%__scm", "%__apply_patch", "%__scm_apply_"+consumer.backend)
			if consumer.backend == gitBackend {
				dependencies = append(dependencies, "%__git")
			}
		}
	}

	if applies && consumer.backend != gitBackend {
		dependencies = append(dependencies, "%__patch", "%_default_patch_flags")
	}

	return slices.ContainsFunc(dependencies, func(name string) bool { return s.redefined[name] })
}

// parsePatchConsumer turns a recognized command into selection and application
// settings, applying command-specific defaults and retaining unsupported behavior.
func parsePatchConsumer(line, backend string) patchConsumer {
	consumer := patchConsumer{command: line, minimum: 0, maximum: -1, strip: -1, backend: backend}

	visible, _, _ := patchShellContext(line)

	tokens, err := shellquote.Split(visible)
	if err != nil {
		consumer.unresolved = fmt.Sprintf("cannot parse patch command arguments: %s", err)

		return consumer
	}

	consumer.macro = tokens[0]
	consumer.removesVendor = consumer.macro == goprepCommand

	consumer.disabled = consumer.macro == setupCommand
	if !isPatchSetup(consumer.macro) && consumer.macro != autopatchCommand && consumer.macro != setupCommand {
		consumer.backend = patchBackend
		if consumer.macro != patchCommand {
			number, _ := strconv.Atoi(strings.TrimPrefix(consumer.macro, patchCommand))
			consumer.numbers = []int{number}
		}
	}

	consumer.parseArguments(tokens)

	if consumer.macro == patchCommand && len(consumer.numbers) == 0 {
		consumer.numbers = []int{0}
	}

	if consumer.macro == goprepCommand {
		consumer.disabled = consumer.strip < 0
	}

	if consumer.macro != autopatchCommand && len(consumer.numbers) > 1 {
		consumer.unresolved = "multiple individual patch selections cannot be resolved"
		consumer.selectionUncertain = true
	}

	if !knownPatchBackend(consumer.backend) {
		consumer.unresolved = "custom patch backend cannot be resolved"
	}

	return consumer
}

// parseArguments dispatches positional selections, grouped flags, and options
// with attached or separate values, recording unsupported or incomplete syntax.
func (c *patchConsumer) parseArguments(tokens []string) {
	values, flags := patchConsumerOptions(c.macro)

	for index := 1; index < len(tokens); index++ {
		token := tokens[index]
		if strings.HasPrefix(token, "#") {
			break
		}

		if !strings.HasPrefix(token, "-") {
			c.parseSelection(token)

			continue
		}

		if len(token) < 2 || !strings.ContainsRune(values, rune(token[1])) {
			if len(token) < 2 || strings.Trim(token[1:], flags) != "" {
				c.unresolved = "unsupported patch command option"
				c.selectionUncertain = true

				break
			}

			c.applyFlags(token[1:])

			continue
		}

		option, value := token[1], token[2:]
		if value == "" {
			index++
			if index == len(tokens) {
				c.unresolved = "patch command option is missing its value"
				c.selectionUncertain = true

				break
			}

			value = tokens[index]
		}

		c.applyOption(option, value)
	}
}

// parseSelection accepts literal non-negative positional patch numbers, rejecting
// positional setup arguments and selections whose number cannot be determined.
func (c *patchConsumer) parseSelection(token string) {
	if c.macro == setupCommand || isPatchSetup(c.macro) {
		c.unresolved = "unexpected positional setup arguments cannot be resolved"
		c.selectionUncertain = true

		return
	}

	number, err := strconv.Atoi(token)
	if err != nil || number < 0 {
		c.unresolved = "patch selection is not a literal non-negative number"
		c.selectionUncertain = true

		return
	}

	c.numbers = append(c.numbers, number)
}

// patchConsumerOptions lists the supported value-taking options and valueless
// flags for each command; membership does not imply every setup mode is modeled.
func patchConsumerOptions(command string) (valueOptions, flagOptions string) {
	switch command {
	case setupCommand:
		return "abn", "cCDTqv"
	case autosetupCommand:
		return "abnSp", "cCDTvN"
	case forgeautosetupCommand:
		return "zSp", "vN"
	case goprepCommand:
		return "zibsSp", "akervA"
	case autopatchCommand:
		return "pmM", "qv"
	default:
		return "pPbBzF", "EZ"
	}
}

// applyFlags handles disabled automatic patching and goprep's vendor/layout
// behavior; other accepted flags do not change the analyzed selection.
func (c *patchConsumer) applyFlags(flags string) {
	if c.macro == autosetupCommand || c.macro == forgeautosetupCommand {
		c.disabled = c.disabled || strings.Contains(flags, "N")
	}

	if c.macro == goprepCommand {
		if strings.ContainsAny(flags, "ae") {
			c.unresolved = "multi-source or no-extraction goprep cannot be resolved"
		}

		if strings.Contains(flags, "k") {
			c.removesVendor = false
		}
	}
}

// applyOption records literal selection, strip, and backend settings. Braced
// directory-name macros remain opaque; unsupported values retain uncertainty.
func (c *patchConsumer) applyOption(option byte, value string) {
	if option == 'n' {
		value = withoutPatchExpansions(value, "%{")
	}

	if strings.ContainsAny(value, "%$;|&<>\\`") {
		c.unresolved = "macro-valued or shell patch command options cannot be resolved"
		c.selectionUncertain = c.selectionUncertain || strings.ContainsRune("PmM", rune(option))

		return
	}

	if c.customSetupOption(option) {
		c.unresolved = "custom or multi-source setup cannot be resolved"

		return
	}

	if option == 'S' {
		c.backend = value

		return
	}

	if !strings.ContainsRune("pPmM", rune(option)) {
		return
	}

	number, err := strconv.Atoi(value)
	if err != nil || number < 0 {
		c.unresolved = "macro-valued or invalid patch command options cannot be resolved"
		c.selectionUncertain = c.selectionUncertain || strings.ContainsRune("PmM", rune(option))

		return
	}

	switch option {
	case 'p':
		c.strip = number
	case 'P':
		c.numbers = append(c.numbers, number)
	case 'm':
		c.minimum = number
	case 'M':
		c.maximum = number
	}
}

// customSetupOption identifies additional-source and wrapper-layout options whose
// effect on the target source tree is outside this analysis.
func (c *patchConsumer) customSetupOption(option byte) bool {
	switch c.macro {
	case forgeautosetupCommand:
		return option == 'z'
	case goprepCommand:
		return strings.ContainsRune("zibs", rune(option))
	case setupCommand, autosetupCommand:
		return strings.ContainsRune("ab", rune(option))
	default:
		return false
	}
}

// selects checks an enabled, understood explicit list or inclusive range. An
// unknown patch number can match only an unrestricted bulk selection.
func (c *patchConsumer) selects(number int) bool {
	if c.disabled || c.selectionUncertain {
		return false
	}

	if len(c.numbers) != 0 {
		return slices.Contains(c.numbers, number)
	}

	if number < 0 {
		return c.minimum == 0 && c.maximum < 0
	}

	return number >= c.minimum && (c.maximum < 0 || number <= c.maximum)
}

// check validates one added patch's remaining declarations and file presence
// before checking consumers; a removed declaration needs no diagnostic.
func (s *patchApplicationScanner) check(patch AddedPatch) *PatchApplicationDiagnostic {
	var declarations []patchDeclaration
	for _, declaration := range s.declarations {
		if declaration.filename == patch.Filename {
			declarations = append(declarations, declaration)
		}
	}

	if len(declarations) == 0 {
		return nil
	}

	diagnostic := &PatchApplicationDiagnostic{Filename: patch.Filename, PatchNumber: declarations[0].number}
	if declarations[0].numberUnknown {
		diagnostic.PatchNumber = -1
	}

	switch {
	case patch.FileMissing:
		diagnostic.Reason = "the patch is still declared, but its final file is missing"
	case len(declarations) != 1 || declarations[0].conditional:
		diagnostic.Reason = "multiple or conditional patch declarations make application uncertain"
	default:
		diagnostic.Reason = s.declarationConflict(patch.Filename, diagnostic.PatchNumber)
	}

	if diagnostic.Reason != "" {
		return diagnostic
	}

	return s.checkConsumers(patch, diagnostic)
}

// declarationConflict reports another filename sharing the added patch's known
// number, without treating unrelated unknown numbers as proven conflicts.
func (s *patchApplicationScanner) declarationConflict(filename string, number int) string {
	for _, other := range s.declarations {
		if other.filename == filename {
			continue
		}

		if !other.numberUnknown && number >= 0 && other.number == number {
			return "the patch number is also used by another declaration"
		}
	}

	return ""
}

// checkConsumers accepts any selecting candidate without a detected problem,
// unless preparation is unsupported; otherwise it returns a targeted warning.
// Multiple selections are alternatives here, not duplicate-application findings.
func (s *patchApplicationScanner) checkConsumers(
	patch AddedPatch, diagnostic *PatchApplicationDiagnostic,
) *PatchApplicationDiagnostic {
	if s.prep != "" {
		diagnostic.Reason = s.prep

		return diagnostic
	}

	for _, consumer := range s.consumers {
		if !consumer.selects(diagnostic.PatchNumber) {
			continue
		}

		reason := consumer.unresolved
		if reason == "" {
			reason = likelyPatchStripMismatch(patch.Content, consumer)
		}

		if reason == "" {
			return nil
		}

		if diagnostic.Reason == "" {
			diagnostic.Command, diagnostic.Reason = consumer.command, reason
		}
	}

	if diagnostic.Reason != "" {
		return diagnostic
	}

	switch {
	case s.prepCount == 0:
		diagnostic.Reason = "no explicit preparation section was found; patch application cannot be verified"
	case diagnostic.PatchNumber < 0:
		diagnostic.Reason = "patch numbering is unknown; no unconditional command selecting all patches was found"
	default:
		diagnostic.Reason = "no recognized command outside conditional branches selects this patch in '%prep'; " +
			"verify the preparation flow before adding an application command"
	}

	return diagnostic
}

// knownPatchBackend identifies backends whose standard behavior is modeled.
func knownPatchBackend(backend string) bool {
	return backend == patchBackend || backend == gitBackend || backend == "gendiff"
}

// likelyPatchStripMismatch inspects diff header paths using the consumer's strip
// level and vendor policy; it neither validates diff contents nor applies hunks.
func likelyPatchStripMismatch(content []byte, consumer patchConsumer) string {
	strip := consumer.strip
	if strip < 0 && consumer.backend == gitBackend {
		strip = 1
	}

	oldRemaining, newRemaining := 0, 0
	contextHunk := false
	lines := strings.Split(string(content), "\n")

	for index, line := range lines {
		if strings.HasPrefix(line, "***************") {
			contextHunk = true

			continue
		}

		if contextHunk {
			if !patchFileBoundary(lines, index) {
				continue
			}

			contextHunk = false
		}

		if strings.HasPrefix(line, "@@ ") {
			oldRemaining, newRemaining = patchHunkLineCounts(line)

			continue
		}

		if oldRemaining != 0 || newRemaining != 0 {
			oldRemaining, newRemaining = advancePatchHunk(line, oldRemaining, newRemaining)

			continue
		}

		if !pairedPatchHeader(lines, index) {
			continue
		}

		if reason := checkPatchHeaderPath(line[4:], strip, consumer.removesVendor); reason != "" {
			return reason
		}
	}

	return ""
}

// pairedPatchHeader excludes isolated header-like prose while preserving unified
// pairs and the checked new-file header of a context diff.
func pairedPatchHeader(lines []string, index int) bool {
	switch {
	case strings.HasPrefix(lines[index], "--- "):
		return (index+1 < len(lines) && strings.HasPrefix(lines[index+1], "+++ ")) ||
			(index > 0 && strings.HasPrefix(lines[index-1], "*** ") &&
				!strings.HasSuffix(lines[index-1], " ****"))
	case strings.HasPrefix(lines[index], "+++ "):
		return index > 0 && strings.HasPrefix(lines[index-1], "--- ")
	default:
		return false
	}
}

// advancePatchHunk consumes a unified-diff body line from its remaining old/new
// counts, leaving metadata such as no-newline markers out of those counts.
func advancePatchHunk(line string, oldCount, newCount int) (oldRemaining, newRemaining int) {
	switch {
	case strings.HasPrefix(line, " "):
		return oldCount - 1, newCount - 1
	case strings.HasPrefix(line, "-"):
		return oldCount - 1, newCount
	case strings.HasPrefix(line, "+"):
		return oldCount, newCount - 1
	default:
		return oldCount, newCount
	}
}

// patchFileBoundary recognizes the next file after a context hunk, distinguishing
// context range markers from file headers and allowing a following unified diff.
func patchFileBoundary(lines []string, index int) bool {
	line := lines[index]

	return strings.HasPrefix(line, "diff ") || strings.HasPrefix(line, "Index: ") ||
		(strings.HasPrefix(line, "*** ") && !strings.HasSuffix(line, " ****")) ||
		(strings.HasPrefix(line, "--- ") && index+1 < len(lines) && strings.HasPrefix(lines[index+1], "+++ "))
}

// patchHunkLineCounts reads unified-diff range lengths so hunk contents cannot
// be mistaken for file headers. Malformed ranges remain opaque until the next hunk.
func patchHunkLineCounts(line string) (oldCount, newCount int) {
	fields := strings.Fields(line)
	if len(fields) < 4 || fields[0] != "@@" || fields[3] != "@@" {
		return -1, -1
	}

	counts := [2]int{1, 1}

	for index, prefix := range []string{"-", "+"} {
		rangeText, found := strings.CutPrefix(fields[index+1], prefix)
		if !found {
			return -1, -1
		}

		start, count, explicit := strings.Cut(rangeText, ",")

		startNumber, err := strconv.Atoi(start)
		if err != nil || startNumber < 0 {
			return -1, -1
		}

		if explicit {
			counts[index], err = strconv.Atoi(count)
			if err != nil || counts[index] < 0 {
				return -1, -1
			}
		}
	}

	return counts[0], counts[1]
}

// checkPatchHeaderPath decodes one diff-header filename and reports likely path
// stripping or vendor-removal problems, ignoring timestamps and '/dev/null'.
func checkPatchHeaderPath(header string, strip int, removesVendor bool) string {
	filename, _, _ := strings.Cut(header, "\t")
	if strings.HasPrefix(filename, "\"") {
		unquoted, err := strconv.Unquote(filename)
		if err != nil {
			return "quoted patch paths cannot be resolved for strip verification"
		}

		filename = unquoted
	}

	if filename == "/dev/null" {
		return ""
	}

	gitPrefix := strings.HasPrefix(filename, "a/") || strings.HasPrefix(filename, "b/")
	if gitPrefix && strip >= 0 && strip != 1 {
		return fmt.Sprintf("patch paths appear to need '-p1', but the command uses '-p%d'; application may fail", strip)
	}

	if strip > strings.Count(filename, "/") {
		return fmt.Sprintf("the command uses '-p%d', which removes the entire patch path; application may fail", strip)
	}

	if strip < 0 && strings.Contains(filename, "/") && (!gitPrefix || strings.Count(filename, "/") > 1) {
		return "patch names subdirectory files, but the command omits '-p'; application may fail"
	}

	if removesVendor && targetsTopLevelVendor(filename, strip) {
		return "goprep removes vendor after applying patches unless '-k' is used"
	}

	return ""
}

// targetsTopLevelVendor checks whether stripping leaves 'vendor' as the first
// path component, where goprep's default vendor cleanup would affect it.
func targetsTopLevelVendor(filename string, strip int) bool {
	parts := strings.Split(filename, "/")

	return strip >= 0 && strip < len(parts) && parts[strip] == "vendor"
}
