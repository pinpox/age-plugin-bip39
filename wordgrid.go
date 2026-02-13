package main

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	bip39 "github.com/tyler-smith/go-bip39"
)

const (
	gridCols  = 3
	gridRows  = 8
	gridTotal = gridCols * gridRows
	cellWidth = 10
)

// renderGridBox renders cells in a 3×8 column-major grid inside a bordered box.
// cells[i] is the pre-rendered content for word i+1 (0-indexed).
// focusedIdx highlights that word's number label (-1 for no highlight).
func renderGridBox(cells []string, focusedIdx int) string {
	dimNum := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	activeNum := lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)

	columns := make([]string, gridCols)
	for col := 0; col < gridCols; col++ {
		var lines []string
		for row := 0; row < gridRows; row++ {
			wordIdx := col*gridRows + row
			num := fmt.Sprintf("%2d. ", wordIdx+1)
			var line string
			if wordIdx == focusedIdx {
				line = activeNum.Render(num)
			} else {
				line = dimNum.Render(num)
			}
			if wordIdx < len(cells) {
				line += cells[wordIdx]
			}
			lines = append(lines, line)
		}
		columns[col] = strings.Join(lines, "\n")
	}

	colGap := lipgloss.NewStyle().PaddingRight(4)
	parts := make([]string, gridCols)
	for i, col := range columns {
		if i < gridCols-1 {
			parts[i] = colGap.Render(col)
		} else {
			parts[i] = col
		}
	}
	grid := lipgloss.JoinHorizontal(lipgloss.Top, parts...)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("212")).
		Padding(1, 2)

	return box.Render(grid)
}

type gridPhase int

const (
	phaseInput  gridPhase = iota
	phaseVerify
)

type wordGridModel struct {
	inputs        []textinput.Model
	focused       int // grid cell with focus, -1 or >=gridTotal when a button is focused
	lastGridFocus int // remembers grid cell when tabbing to buttons
	phase         gridPhase
	generated     []string

	// Verify phase: editIdx cycles through fields then buttons.
	// 0..len(verifyPos)-1 = input fields, len(verifyPos) = Back, len(verifyPos)+1 = Verify.
	verifyPos []int
	editIdx   int

	done    bool
	aborted bool
	err     string
}

func newWordGridModel(generated []string) wordGridModel {
	fieldBg := lipgloss.NewStyle().Background(lipgloss.Color("236"))
	placeholderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("243")).
		Background(lipgloss.Color("236"))

	inputs := make([]textinput.Model, gridTotal)
	for i := range inputs {
		t := textinput.New()
		t.Prompt = ""
		t.CharLimit = 20
		t.Width = cellWidth
		t.TextStyle = fieldBg
		t.PlaceholderStyle = placeholderStyle
		t.Cursor.TextStyle = fieldBg
		if i < len(generated) {
			t.Placeholder = generated[i]
		}
		inputs[i] = t
	}
	inputs[0].Focus()

	return wordGridModel{
		inputs:    inputs,
		generated: generated,
	}
}

func (m wordGridModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m wordGridModel) effectiveWords() []string {
	words := make([]string, gridTotal)
	for i, input := range m.inputs {
		v := strings.TrimSpace(input.Value())
		if v != "" {
			words[i] = strings.ToLower(v)
		} else if i < len(m.generated) {
			words[i] = m.generated[i]
		}
	}
	return words
}

func (m wordGridModel) mnemonic() string {
	var nonEmpty []string
	for _, w := range m.effectiveWords() {
		if w != "" {
			nonEmpty = append(nonEmpty, w)
		}
	}
	return strings.Join(nonEmpty, " ")
}

func (m wordGridModel) allUserProvided() bool {
	for _, input := range m.inputs {
		if strings.TrimSpace(input.Value()) == "" {
			return false
		}
	}
	return true
}

func (m wordGridModel) anyUserEdited() bool {
	for _, input := range m.inputs {
		if strings.TrimSpace(input.Value()) != "" {
			return true
		}
	}
	return false
}

func (m wordGridModel) allWordsValid() bool {
	for _, w := range m.effectiveWords() {
		if w == "" {
			return false
		}
		if _, ok := bip39.GetWordIndex(w); !ok {
			return false
		}
	}
	return true
}

func (m wordGridModel) isEditable(idx int) bool {
	if m.phase == phaseInput {
		return true
	}
	for _, p := range m.verifyPos {
		if p == idx {
			return true
		}
	}
	return false
}

// verifyItemCount returns the total number of focusable items in verify phase
// (input fields + Back button + Verify button).
func (m wordGridModel) verifyItemCount() int {
	return len(m.verifyPos) + 2
}

func (m wordGridModel) onBackButton() bool {
	return m.phase == phaseVerify && m.editIdx == len(m.verifyPos)
}

func (m wordGridModel) onVerifyButton() bool {
	return m.phase == phaseVerify && m.editIdx == len(m.verifyPos)+1
}

func (m wordGridModel) onRegenerateButton() bool {
	return m.phase == phaseInput && m.focused == gridTotal
}

func (m wordGridModel) onContinueButton() bool {
	return m.phase == phaseInput && m.focused == gridTotal+1
}

// inputItemCount returns the total focusable items in input phase
// (grid cells + Regenerate + Continue).
func (m wordGridModel) inputItemCount() int {
	return gridTotal + 2
}

func (m wordGridModel) onButton() bool {
	return m.onBackButton() || m.onVerifyButton() || m.onRegenerateButton() || m.onContinueButton()
}

func (m wordGridModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.aborted = true
			return m, tea.Quit

		case tea.KeyTab:
			if m.phase == phaseVerify {
				m.editIdx = (m.editIdx + 1) % m.verifyItemCount()
				m.syncVerifyFocus()
			} else if m.focused >= 0 && m.focused < gridTotal {
				// Grid → first button
				m.lastGridFocus = m.focused
				m.focused = gridTotal
			} else if m.focused == gridTotal {
				// Regenerate → Continue
				m.focused = gridTotal + 1
			} else {
				// Continue → back to grid
				m.focused = m.lastGridFocus
			}
			return m, m.focusCmd()

		case tea.KeyShiftTab:
			if m.phase == phaseVerify {
				m.editIdx = (m.editIdx - 1 + m.verifyItemCount()) % m.verifyItemCount()
				m.syncVerifyFocus()
			} else if m.focused >= 0 && m.focused < gridTotal {
				// Grid → last button
				m.lastGridFocus = m.focused
				m.focused = gridTotal + 1
			} else if m.focused == gridTotal+1 {
				// Continue → Regenerate
				m.focused = gridTotal
			} else {
				// Regenerate → back to grid
				m.focused = m.lastGridFocus
			}
			return m, m.focusCmd()

		// Column-major arrow navigation (input phase grid only).
		case tea.KeyUp:
			if m.phase == phaseInput && m.focused >= 0 && m.focused < gridTotal && m.focused%gridRows > 0 {
				m.focused--
				return m, m.focusCmd()
			}

		case tea.KeyDown:
			if m.phase == phaseInput && m.focused >= 0 && m.focused < gridTotal && m.focused%gridRows < gridRows-1 {
				m.focused++
				return m, m.focusCmd()
			}

		case tea.KeyLeft:
			if m.phase == phaseInput && m.focused >= gridRows && m.focused < gridTotal {
				m.focused -= gridRows
				return m, m.focusCmd()
			}
			if m.onVerifyButton() {
				m.editIdx--
				m.syncVerifyFocus()
				return m, m.focusCmd()
			}

		case tea.KeyRight:
			if m.phase == phaseInput && m.focused >= 0 && m.focused+gridRows < gridTotal {
				m.focused += gridRows
				return m, m.focusCmd()
			}
			if m.onBackButton() {
				m.editIdx++
				m.syncVerifyFocus()
				return m, m.focusCmd()
			}

		case tea.KeyEnter:
			return m.handleEnter()
		}
	}

	// Only forward events to an input if one is focused.
	if m.focused >= 0 && m.focused < gridTotal {
		// Clear validation error on actual keypresses (not cursor blink etc.)
		if _, isKey := msg.(tea.KeyMsg); isKey {
			m.err = ""
		}
		var cmd tea.Cmd
		m.inputs[m.focused], cmd = m.inputs[m.focused].Update(msg)
		m.updateInputStyle(m.focused)
		return m, cmd
	}
	return m, nil
}

// updateInputStyle sets the text style of the given input based on whether
// its current value is a valid BIP39 word. Invalid/incomplete words are red.
func (m *wordGridModel) updateInputStyle(idx int) {
	fieldBg := lipgloss.NewStyle().Background(lipgloss.Color("236"))
	invalidStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("196")).
		Background(lipgloss.Color("236"))

	val := strings.TrimSpace(m.inputs[idx].Value())
	if val != "" {
		if _, ok := bip39.GetWordIndex(strings.ToLower(val)); !ok {
			m.inputs[idx].TextStyle = invalidStyle
			m.inputs[idx].Cursor.TextStyle = invalidStyle
		} else {
			m.inputs[idx].TextStyle = fieldBg
			m.inputs[idx].Cursor.TextStyle = fieldBg
		}
	} else {
		m.inputs[idx].TextStyle = fieldBg
		m.inputs[idx].Cursor.TextStyle = fieldBg
	}
}

// syncVerifyFocus sets m.focused based on the current editIdx.
func (m *wordGridModel) syncVerifyFocus() {
	if m.editIdx < len(m.verifyPos) {
		m.focused = m.verifyPos[m.editIdx]
	} else {
		m.focused = -1 // on a button
	}
}

func (m wordGridModel) handleEnter() (tea.Model, tea.Cmd) {
	if m.phase == phaseInput {
		if m.onRegenerateButton() {
			return m.regenerate()
		}
		if m.onContinueButton() {
			return m.handleContinue()
		}
		// On a grid cell — jump to Continue button.
		m.lastGridFocus = m.focused
		m.focused = gridTotal + 1 // Continue
		return m, m.focusCmd()
	}

	// Verify phase.
	if m.onBackButton() {
		// Restore placeholders and return to input phase.
		for _, pos := range m.verifyPos {
			m.inputs[pos].SetValue("")
			m.inputs[pos].Placeholder = m.generated[pos]
		}
		m.phase = phaseInput
		m.verifyPos = nil
		m.editIdx = 0
		m.focused = 0
		m.err = ""
		return m, m.focusCmd()
	}

	if m.onVerifyButton() {
		// Validate the two words.
		for _, pos := range m.verifyPos {
			got := strings.ToLower(strings.TrimSpace(m.inputs[pos].Value()))
			if got != m.generated[pos] {
				m.err = fmt.Sprintf("Word #%d is incorrect — try again.", pos+1)
				return m, nil
			}
		}
		m.done = true
		return m, tea.Quit
	}

	// On a verify field — jump to Verify button.
	m.editIdx = len(m.verifyPos) + 1
	m.syncVerifyFocus()
	return m, m.focusCmd()
}

func (m wordGridModel) handleContinue() (tea.Model, tea.Cmd) {
	if !m.allWordsValid() {
		m.err = "Some words are not valid BIP39 words."
		return m, nil
	}

	mn := m.mnemonic()

	// If the user edited any words, accept directly (skip verify).
	// The full mnemonic checksum is validated during key derivation.
	if m.anyUserEdited() {
		if !bip39.IsMnemonicValid(mn) {
			m.err = "Invalid BIP39 checksum — these words don't form a valid mnemonic."
			return m, nil
		}
		m.done = true
		return m, tea.Quit
	}

	// Pure generated phrase — transition to verify.
	pos1, pos2, err := pickTwoRandomPositions(len(m.generated))
	if err != nil {
		m.err = err.Error()
		return m, nil
	}
	editable := []int{pos1, pos2}
	sort.Ints(editable)

	m.phase = phaseVerify
	m.verifyPos = editable
	m.editIdx = 0
	m.focused = editable[0]
	m.err = ""

	for _, pos := range editable {
		m.inputs[pos].SetValue("")
		m.inputs[pos].Placeholder = ""
	}

	return m, m.focusCmd()
}

func (m wordGridModel) regenerate() (tea.Model, tea.Cmd) {
	entropy, err := bip39.NewEntropy(256)
	if err != nil {
		m.err = fmt.Sprintf("failed to generate entropy: %v", err)
		return m, nil
	}
	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		m.err = fmt.Sprintf("failed to generate mnemonic: %v", err)
		return m, nil
	}

	words := strings.Split(mnemonic, " ")
	m.generated = words
	for i := range m.inputs {
		m.inputs[i].SetValue("")
		if i < len(words) {
			m.inputs[i].Placeholder = words[i]
		} else {
			m.inputs[i].Placeholder = ""
		}
	}
	m.focused = 0
	m.err = ""
	return m, m.focusCmd()
}

func (m *wordGridModel) focusCmd() tea.Cmd {
	cmds := make([]tea.Cmd, 0, gridTotal)
	for i := range m.inputs {
		if i == m.focused {
			cmds = append(cmds, m.inputs[i].Focus())
		} else {
			m.inputs[i].Blur()
		}
	}
	return tea.Batch(cmds...)
}

func (m wordGridModel) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	fieldBg := lipgloss.NewStyle().Background(lipgloss.Color("236"))
	maskedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

	cells := make([]string, gridTotal)
	for i := 0; i < gridTotal; i++ {
		if m.isEditable(i) {
			cells[i] = fieldBg.Render(m.inputs[i].View())
		} else {
			cells[i] = maskedStyle.Render(fmt.Sprintf("%-*s", cellWidth+1, "******"))
		}
	}

	title := "Deriving age key from BIP39 seed phrase"
	var desc string
	if m.phase == phaseVerify {
		desc = "Confirm your backup by entering the highlighted words."
	} else {
		desc = "Accept the generated phrase or enter your own words."
	}
	help := "tab/arrows navigate • enter accept • esc quit"

	var b strings.Builder
	b.WriteString(titleStyle.Render(title))
	b.WriteString("\n\n")
	if m.err != "" {
		b.WriteString(errStyle.Render(m.err))
	} else {
		b.WriteString(descStyle.Render(desc))
	}
	b.WriteString("\n\n")
	b.WriteString(renderGridBox(cells, m.focused))
	b.WriteString("\n\n")
	b.WriteString(m.renderButtons())
	help = "tab/arrows navigate • enter select • esc quit"

	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render(help))

	return b.String()
}

func (m wordGridModel) renderButtons() string {
	focusedBtn := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("255")).
		Background(lipgloss.Color("63")).
		Padding(0, 2)

	blurredBtn := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Background(lipgloss.Color("235")).
		Padding(0, 2)

	btn := func(label string, active bool) string {
		if active {
			return focusedBtn.Render(label)
		}
		return blurredBtn.Render(label)
	}

	if m.phase == phaseVerify {
		return btn("Back", m.onBackButton()) + "  " + btn("Verify", m.onVerifyButton())
	}
	return btn("Regenerate", m.onRegenerateButton()) + "  " + btn("Continue", m.onContinueButton())
}

func runWordGrid(generated []string) (string, error) {
	p := tea.NewProgram(newWordGridModel(generated), tea.WithOutput(os.Stderr))
	finalModel, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("input failed: %w", err)
	}
	m := finalModel.(wordGridModel)
	if m.aborted {
		return "", fmt.Errorf("aborted")
	}
	return m.mnemonic(), nil
}

func pickTwoRandomPositions(n int) (int, int, error) {
	idx1, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0, 0, fmt.Errorf("failed to generate random index: %w", err)
	}
	var idx2 *big.Int
	for {
		idx2, err = rand.Int(rand.Reader, big.NewInt(int64(n)))
		if err != nil {
			return 0, 0, fmt.Errorf("failed to generate random index: %w", err)
		}
		if idx2.Int64() != idx1.Int64() {
			break
		}
	}
	return int(idx1.Int64()), int(idx2.Int64()), nil
}
