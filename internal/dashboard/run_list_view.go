package dashboard

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// runListView extends List's primary-row selection to the model/time row.
type runListView struct {
	*tview.List
	dragging bool
}

func (view *runListView) Draw(screen tcell.Screen) {
	view.List.Draw(screen)
	defer view.drawScrollbar(screen)
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

func (view *runListView) drawScrollbar(screen tcell.Screen) {
	x, y, width, height := view.GetInnerRect()
	count := view.GetItemCount()
	visible := max(1, height/2)
	if height < 1 || count <= visible {
		return
	}
	offset, _ := view.GetOffset()
	thumb := max(1, height*visible/count)
	start := min(height-thumb, offset*(height-thumb)/(count-visible))
	for row := range height {
		char, color := '░', tcell.ColorGray
		if row >= start && row < start+thumb {
			char, color = '█', tcell.ColorAqua
		}
		screen.SetContent(x+width, y+row, char, nil, tcell.StyleDefault.Foreground(color))
	}
}

func (view *runListView) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(tview.Primitive)) (bool, tview.Primitive) {
		x, y := event.Position()
		left, top, width, height := view.GetInnerRect()
		onRail := x == left+width && y >= top && y < top+height && view.GetItemCount() > max(1, height/2)
		if !onRail && !view.dragging {
			return view.List.MouseHandler()(action, event, setFocus)
		}
		switch action {
		case tview.MouseLeftDown:
			view.dragging = true
		case tview.MouseLeftUp:
			view.dragging = false
		case tview.MouseLeftClick:
		case tview.MouseMove:
			if !view.dragging {
				return false, nil
			}
		default:
			return view.List.MouseHandler()(action, event, setFocus)
		}
		setFocus(view.List)
		row := min(max(0, y-top), max(0, height-1))
		view.SetCurrentItem(row * (view.GetItemCount() - 1) / max(1, height-1))
		if view.dragging {
			return true, view
		}
		return true, nil
	}
}
