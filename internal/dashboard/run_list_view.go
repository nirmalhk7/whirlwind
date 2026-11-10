package dashboard

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// runListView extends List's primary-row selection to the model/time row.
type runListView struct{ *tview.List }

func (view *runListView) Draw(screen tcell.Screen) {
	view.List.Draw(screen)
	if view.GetItemCount() == 0 {
		return
	}
	x, y, width, height := view.GetInnerRect()
	offset, _ := view.GetOffset()
	row := 2 * (view.GetCurrentItem() - offset)
	if row < 0 || row+1 >= height || width == 0 {
		return
	}
	_, _, selected, _ := screen.GetContent(x, y+row)
	for column := 0; column < width; column++ {
		char, combining, _, _ := screen.GetContent(x+column, y+row+1)
		screen.SetContent(x+column, y+row+1, char, combining, selected)
	}
}
