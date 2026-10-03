package tui

import (
	"fmt"

	"github.com/mattn/go-runewidth"
)

type composerVisualPosition struct {
	row    int
	column int
}

func composerCaretPosition(row, column, width int) composerVisualPosition {
	if column+1 > width && column > 0 {
		return composerVisualPosition{row: row + 1}
	}
	return composerVisualPosition{row: row, column: column}
}

// composerVisualPositions maps every logical insertion point to the row and
// display column used by wrap. Carets at automatic wrap boundaries belong to
// the next row, just as they do when the following rune is wrapped.
func composerVisualPositions(input []rune, width int) []composerVisualPosition {
	width = max(1, width)
	positions := make([]composerVisualPosition, len(input)+1)
	row, column := 0, 0
	flush := func() {
		row++
		column = 0
	}

	for i, r := range input {
		if r == '\n' {
			positions[i] = composerCaretPosition(row, column, width)
			flush()
			continue
		}

		if hiddenReviewRune(r) {
			// wrap displays controls and zero-width runes as a visible escape.
			// Account for each escape character separately, including wraps that
			// happen in the middle of the escape.
			escaped := fmt.Sprintf("\\u%04X", r)
			positions[i] = composerCaretPosition(row, column, width)
			for range escaped {
				if column+1 > width && column > 0 {
					flush()
				}
				column++
			}
			continue
		}

		positions[i] = composerCaretPosition(row, column, width)
		runeWidth := runewidth.RuneWidth(r)
		if column+runeWidth > width && column > 0 {
			flush()
		}
		column += runeWidth
	}

	// The composer inserts a one-cell block caret before wrapping.
	positions[len(input)] = composerCaretPosition(row, column, width)
	return positions
}

// composerVisualRow returns the wrapped row containing the insertion point.
// Caret indices outside the input are clamped to its valid insertion points.
func composerVisualRow(input []rune, caret, width int) int {
	caret = min(len(input), max(0, caret))
	return composerVisualPositions(input, width)[caret].row
}

// moveComposerCaretVertical moves one wrapped row, choosing the insertion
// point nearest the current display column. A wide rune has no insertion
// point inside its cells; ties therefore resolve to the point on its left.
// The preferred column is retained by composerVerticalMover across a sequence
// of moves; this convenience function starts a fresh sequence for each call.
func moveComposerCaretVertical(input []rune, caret, width, direction int) (newCaret int, moved bool) {
	caret = min(len(input), max(0, caret))
	positions := composerVisualPositions(input, width)
	return moveComposerCaretVerticalToColumn(input, caret, direction, positions[caret].column, positions)
}

func moveComposerCaretVerticalToColumn(input []rune, caret, direction, preferredColumn int, positions []composerVisualPosition) (newCaret int, moved bool) {
	caret = min(len(input), max(0, caret))
	if direction == 0 {
		return caret, false
	}
	step := 1
	if direction < 0 {
		step = -1
	}

	currentRow := positions[caret].row
	targetRow := currentRow + step
	lastRow := positions[len(input)].row
	// A long escaped control can occupy display rows that have no corresponding
	// logical insertion point. Skip those rows rather than stranding movement.
	for targetRow >= 0 && targetRow <= lastRow {
		bestCaret, bestDistance, bestColumn := -1, int(^uint(0)>>1), int(^uint(0)>>1)
		for i, position := range positions {
			if position.row != targetRow {
				continue
			}
			distance := position.column - preferredColumn
			if distance < 0 {
				distance = -distance
			}
			if distance < bestDistance || (distance == bestDistance && position.column < bestColumn) {
				bestCaret, bestDistance, bestColumn = i, distance, position.column
			}
		}
		if bestCaret >= 0 {
			return bestCaret, bestCaret != caret
		}
		targetRow += step
	}
	return caret, false
}

// composerVerticalMover retains a preferred display column across Up/Down
// presses. Call reset after horizontal movement, edits, or a width change.
type composerVerticalMover struct {
	preferredColumn    int
	hasPreferredColumn bool
}

func (m *composerVerticalMover) reset() {
	m.preferredColumn = 0
	m.hasPreferredColumn = false
}

func (m *composerVerticalMover) move(input []rune, caret, width, direction int) (newCaret int, moved bool) {
	caret = min(len(input), max(0, caret))
	positions := composerVisualPositions(input, width)
	if !m.hasPreferredColumn {
		m.preferredColumn = positions[caret].column
		m.hasPreferredColumn = true
	}
	return moveComposerCaretVerticalToColumn(input, caret, direction, m.preferredColumn, positions)
}
