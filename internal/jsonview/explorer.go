package jsonview

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/reflow/truncate"
	"github.com/muesli/reflow/wordwrap"
	"github.com/tidwall/gjson"
)

const (
	// UI layout constants
	borderPadding     = 2
	heightOffset      = 5
	tableMinHeight    = 2
	titlePaddingLeft  = 2
	titlePaddingTop   = 0
	footerPaddingLeft = 1

	// Column width constants
	defaultColumnWidth = 10
	keyColumnWidth     = 3
	valueColumnWidth   = 5

	// String formatting constants
	maxStringLength  = 100
	maxPreviewLength = 24

	arrayColor  = lipgloss.Color("1")
	stringColor = lipgloss.Color("5")
	objectColor = lipgloss.Color("4")
)

type keyMap struct {
	Up         key.Binding
	Down       key.Binding
	Enter      key.Binding
	Back       key.Binding
	PrintValue key.Binding
	Raw        key.Binding
	Quit       key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Quit, k.Up, k.Down, k.Back, k.Enter, k.PrintValue, k.Raw}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{k.ShortHelp()}
}

var keys = keyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	Back: key.NewBinding(
		key.WithKeys("left", "h", "backspace"),
		key.WithHelp("←/h", "go back"),
	),
	Enter: key.NewBinding(
		key.WithKeys("right", "l"),
		key.WithHelp("→/l", "expand"),
	),
	PrintValue: key.NewBinding(
		key.WithKeys("p"),
		key.WithHelp("p", "print and exit"),
	),
	Raw: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "toggle raw JSON"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "esc", "ctrl+c", "enter"),
		key.WithHelp("q/enter", "quit"),
	),
}

var (
	titleStyle         = lipgloss.NewStyle().Bold(true).PaddingLeft(titlePaddingLeft).PaddingTop(titlePaddingTop)
	arrayStyle         = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(arrayColor)
	stringStyle        = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(stringColor)
	objectStyle        = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(objectColor)
	stringLiteralStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))
)

type JSONView interface {
	GetPath() string
	GetData() gjson.Result
	Update(tea.Msg, bool) tea.Cmd
	View() string
	Resize(width, height int)
}

type TableView struct {
	width     int
	height    int
	path      string
	data      gjson.Result
	table     table.Model
	rowData   []gjson.Result
	iterator  AnyIterator
	isLoading bool
	columns   []table.Column
}

func (tv *TableView) GetPath() string       { return tv.path }
func (tv *TableView) GetData() gjson.Result { return tv.data }
func (tv *TableView) View() string          { return tv.table.View() }

func (tv *TableView) Update(msg tea.Msg, raw bool) tea.Cmd {
	var cmd tea.Cmd
	tv.table, cmd = tv.table.Update(msg)

	// Check if we need to load more data
	if tv.iterator != nil && !tv.isLoading && tv.data.IsArray() {
		cursor := tv.table.Cursor()
		totalRows := len(tv.table.Rows())

		// Load more when we're at the last row
		if cursor == totalRows-1 {
			tv.isLoading = true
			return tv.loadMoreData(raw)
		}
	}

	return cmd
}

func (tv *TableView) loadMoreData(raw bool) tea.Cmd {
	return func() tea.Msg {
		if tv.iterator == nil {
			return nil
		}

		if !tv.iterator.Next() {
			tv.isLoading = false
			return tv.iterator.Err()
		}

		obj := tv.iterator.Current()
		var result gjson.Result
		if jsonBytes, err := json.Marshal(obj); err != nil {
			return err
		} else {
			result = gjson.ParseBytes(jsonBytes)
		}

		if !result.Exists() {
			tv.isLoading = false
			return nil
		}

		// Add the new item to our data
		tv.rowData = append(tv.rowData, result)

		// Add new row to the table
		newRow := table.Row{formatValue(result, raw)}

		// For array of objects, we need to format according to columns
		if len(tv.columns) > 1 && result.IsObject() {
			newRow = make(table.Row, len(tv.columns))
			for i, col := range tv.columns {
				newRow[i] = formatValue(result.Get(col.Title), raw)
			}
		}

		rows := tv.table.Rows()
		rows = append(rows, newRow)
		tv.table.SetRows(rows)

		// Resize columns to accommodate the new data
		tv.Resize(tv.width, tv.height)

		tv.isLoading = false
		return nil
	}
}

func (tv *TableView) Resize(width, height int) {
	tv.width = width
	tv.height = height
	tv.updateColumnWidths(width)
	tv.table.SetHeight(min(height-heightOffset, tableMinHeight+len(tv.table.Rows())))
}

func (tv *TableView) updateColumnWidths(width int) {
	columns := tv.table.Columns()
	widths := make([]int, len(columns))

	// Calculate required widths from headers and content
	for i, col := range columns {
		widths[i] = lipgloss.Width(col.Title)
	}

	for _, row := range tv.table.Rows() {
		for i, cell := range row {
			if i < len(widths) {
				widths[i] = max(widths[i], lipgloss.Width(cell))
			}
		}
	}

	totalWidth := sum(widths)
	available := width - borderPadding*len(columns)

	if totalWidth <= available {
		for i, w := range widths {
			columns[i].Width = w
		}
		return
	}

	fairShare := float64(available) / float64(len(columns))
	shrinkable := 0.0

	for _, w := range widths {
		if float64(w) > fairShare {
			shrinkable += float64(w) - fairShare
		}
	}

	if shrinkable > 0 {
		excess := float64(totalWidth - available)
		for i, w := range widths {
			if float64(w) > fairShare {
				reduction := (float64(w) - fairShare) * (excess / shrinkable)
				widths[i] = int(math.Round(float64(w) - reduction))
			}
		}
	}

	for i, w := range widths {
		columns[i].Width = w
	}

	tv.table.SetColumns(columns)
}

type TextView struct {
	path     string
	data     gjson.Result
	viewport viewport.Model
	ready    bool
}

func (tv *TextView) GetPath() string       { return tv.path }
func (tv *TextView) GetData() gjson.Result { return tv.data }
func (tv *TextView) View() string          { return tv.viewport.View() }

func (tv *TextView) Update(msg tea.Msg, raw bool) tea.Cmd {
	var cmd tea.Cmd
	tv.viewport, cmd = tv.viewport.Update(msg)
	return cmd
}

func (tv *TextView) Resize(width, height int) {
	h := height - heightOffset
	if !tv.ready {
		tv.viewport = viewport.New(width, h)
		tv.viewport.SetContent(wordwrap.String(tv.data.String(), width))
		tv.ready = true
		return
	}
	tv.viewport.Width = width
	tv.viewport.Height = h
}

type JSONViewer struct {
	stack   []JSONView
	root    string
	width   int
	height  int
	rawMode bool
	message string
	help    help.Model
}

// ExploreJSON explores a single JSON value known ahead of time
func ExploreJSON(title string, json gjson.Result) error {
	view, err := newView("", json, false)
	if err != nil {
		return err
	}

	viewer := &JSONViewer{stack: []JSONView{view}, root: title, rawMode: false, help: help.New()}

	_, err = tea.NewProgram(viewer).Run()
	if viewer.message != "" {
		_, msgErr := fmt.Println("\n" + viewer.message)
		err = errors.Join(err, msgErr)
	}
	return err
}

type hasRawJSON interface {
	RawJSON() string
}

// ExploreJSONStream explores JSON data loaded incrementally via an iterator
func ExploreJSONStream[T any](title string, it Iterator[T]) error {
	anyIt := genericToAnyIterator(it)

	preloadCount := 20
	if termHeight, _, err := term.GetSize(os.Stdout.Fd()); err == nil {
		preloadCount = termHeight
	}

	items := make([]any, 0, preloadCount)
	for i := 0; i < preloadCount && anyIt.Next(); i++ {
		items = append(items, anyIt.Current())
	}

	if err := anyIt.Err(); err != nil {
		return err
	}

	arrayJSONBytes, err := marshalItemsToJSONArray(items)
	if err != nil {
		return err
	}

	arrayJSON := gjson.ParseBytes(arrayJSONBytes)
	view, err := newTableView("", arrayJSON, false)
	if err != nil {
		return err
	}

	// Set iterator if there might be more data
	if len(items) == preloadCount {
		view.iterator = anyIt
	}

	viewer := &JSONViewer{stack: []JSONView{view}, root: title, rawMode: false, help: help.New()}
	_, err = tea.NewProgram(viewer).Run()
	if viewer.message != "" {
		_, msgErr := fmt.Println("\n" + viewer.message)
		err = errors.Join(err, msgErr)
	}
	return err
}

func marshalItemsToJSONArray(items []any) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('[')

	for i, item := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		if hasRaw, ok := item.(hasRawJSON); ok {
			buf.WriteString(hasRaw.RawJSON())
		} else {
			jsonData, err := json.Marshal(item)
			if err != nil {
				return nil, err
			}
			buf.Write(jsonData)
		}
	}

	buf.WriteByte(']')
	return buf.Bytes(), nil
}

func (v *JSONViewer) current() JSONView { return v.stack[len(v.stack)-1] }
func (v *JSONViewer) Init() tea.Cmd     { return nil }

func (v *JSONViewer) resize(width, height int) {
	v.width, v.height = width, height
	v.help.Width = width
	for i := range v.stack {
		v.stack[i].Resize(width, height)
	}
}

func (v *JSONViewer) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.resize(msg.Width-borderPadding, msg.Height)
		return v, nil
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.Quit):
			return v, tea.Quit
		case key.Matches(msg, keys.Enter):
			return v.navigateForward()
		case key.Matches(msg, keys.Back):
			return v.navigateBack()
		case key.Matches(msg, keys.Raw):
			return v.toggleRaw()
		case key.Matches(msg, keys.PrintValue):
			v.message = v.getSelectedContent()
			return v, tea.Quit
		}
	}

	return v, v.current().Update(msg, v.rawMode)
}

func (v *JSONViewer) getSelectedContent() string {
	tableView, ok := v.current().(*TableView)
	if !ok {
		return v.current().GetData().Raw
	}

	selected := tableView.rowData[tableView.table.Cursor()]
	if selected.Type == gjson.String {
		return selected.String()
	}
	return selected.Raw
}

func (v *JSONViewer) navigateForward() (tea.Model, tea.Cmd) {
	tableView, ok := v.current().(*TableView)
	if !ok {
		return v, nil
	}

	if len(tableView.rowData) < 1 {
		return v, nil
	}

	cursor := tableView.table.Cursor()
	selected := tableView.rowData[cursor]
	if !v.canNavigateInto(selected) {
		return v, nil
	}

	path := v.buildNavigationPath(tableView, cursor)
	forwardView, err := newView(path, selected, v.rawMode)
	if err != nil {
		return v, nil
	}

	v.stack = append(v.stack, forwardView)
	v.resize(v.width, v.height)
	return v, nil
}

func (v *JSONViewer) buildNavigationPath(tableView *TableView, cursor int) string {
	if tableView.data.IsArray() {
		return fmt.Sprintf("%s[%d]", tableView.path, cursor)
	}
	key := tableView.data.Get("@keys").Array()[cursor].Str
	return fmt.Sprintf("%s[%s]", tableView.path, quoteString(key))
}

func quoteString(s string) string {
	// Replace backslashes and quotes with escaped versions
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return stringLiteralStyle.Render("\"" + s + "\"")
}

func (v *JSONViewer) canNavigateInto(data gjson.Result) bool {
	switch {
	case data.IsArray():
		return len(data.Array()) > 0
	case data.IsObject():
		return len(data.Map()) > 0
	case data.Type == gjson.String:
		str := data.String()
		return strings.Contains(str, "\n") || lipgloss.Width(str) >= maxStringLength
	}
	return false
}

func (v *JSONViewer) navigateBack() (tea.Model, tea.Cmd) {
	if len(v.stack) > 1 {
		v.stack = v.stack[:len(v.stack)-1]
	}
	return v, nil
}

func (v *JSONViewer) toggleRaw() (tea.Model, tea.Cmd) {
	v.rawMode = !v.rawMode

	for i, view := range v.stack {
		viewWithRaw, err := newView(view.GetPath(), view.GetData(), v.rawMode)
		if err != nil {
			return v, tea.Printf("Error: %s", err)
		}
		if newTV, ok := viewWithRaw.(*TableView); ok {
			if tv, ok := view.(*TableView); ok && tv.iterator != nil {
				newTV.iterator = tv.iterator
			}
		}
		v.stack[i] = viewWithRaw
	}

	v.resize(v.width, v.height)
	return v, nil
}

func (v *JSONViewer) View() string {
	view := v.current()
	title := v.buildTitle(view)
	content := titleStyle.Render(title)
	style := v.getStyleForData(view.GetData())
	content += "\n" + style.Render(view.View())
	content += "\n" + v.help.View(keys)
	return content
}

func (v *JSONViewer) buildTitle(view JSONView) string {
	title := v.root
	if len(view.GetPath()) > 0 {
		title += " → " + view.GetPath()
	}
	if v.rawMode {
		title += " (JSON)"
	}
	return title
}

func (v *JSONViewer) getStyleForData(data gjson.Result) lipgloss.Style {
	switch {
	case data.Type == gjson.String:
		return stringStyle
	case data.IsArray():
		return arrayStyle
	default:
		return objectStyle
	}
}

func newView(path string, data gjson.Result, raw bool) (JSONView, error) {
	if data.Type == gjson.String {
		return newTextView(path, data)
	}
	return newTableView(path, data, raw)
}

func newTextView(path string, data gjson.Result) (*TextView, error) {
	if !data.Exists() || data.Type != gjson.String {
		return nil, fmt.Errorf("invalid text JSON")
	}
	return &TextView{path: path, data: data}, nil
}

func newTableView(path string, data gjson.Result, raw bool) (*TableView, error) {
	if !data.Exists() || data.Type != gjson.JSON {
		return nil, fmt.Errorf("invalid table JSON")
	}

	switch {
	case data.IsArray():
		array := data.Array()
		if isArrayOfObjects(array) {
			return newArrayOfObjectsTableView(path, data, array, raw), nil
		} else {
			return newArrayTableView(path, data, array, raw), nil
		}
	case data.IsObject():
		return newObjectTableView(path, data, raw), nil
	default:
		return nil, fmt.Errorf("unsupported JSON type")
	}
}

func newArrayTableView(path string, data gjson.Result, array []gjson.Result, raw bool) *TableView {
	columns := []table.Column{{Title: "Items", Width: defaultColumnWidth}}
	rows := make([]table.Row, 0, len(array))
	rowData := make([]gjson.Result, 0, len(array))

	for _, item := range array {
		rows = append(rows, table.Row{formatValue(item, raw)})
		rowData = append(rowData, item)
	}

	t := createTable(columns, rows, arrayColor)
	return &TableView{
		path:    path,
		data:    data,
		table:   t,
		rowData: rowData,
		columns: columns,
	}
}

func newArrayOfObjectsTableView(path string, data gjson.Result, array []gjson.Result, raw bool) *TableView {
	// Collect unique keys
	keySet := make(map[string]struct{})
	var columns []table.Column

	for _, item := range array {
		for _, key := range item.Get("@keys").Array() {
			if _, exists := keySet[key.Str]; !exists {
				keySet[key.Str] = struct{}{}
				title := key.Str
				columns = append(columns, table.Column{Title: title, Width: defaultColumnWidth})
			}
		}
	}

	rows := make([]table.Row, 0, len(array))
	rowData := make([]gjson.Result, 0, len(array))

	for _, item := range array {
		row := make(table.Row, len(columns))
		for i, col := range columns {
			row[i] = formatValue(item.Get(col.Title), raw)
		}
		rows = append(rows, row)
		rowData = append(rowData, item)
	}

	t := createTable(columns, rows, arrayColor)
	return &TableView{
		path:    path,
		data:    data,
		table:   t,
		rowData: rowData,
		columns: columns,
	}
}

func newObjectTableView(path string, data gjson.Result, raw bool) *TableView {
	columns := []table.Column{{Title: "Object"}, {}}

	keys := data.Get("@keys").Array()
	rows := make([]table.Row, 0, len(keys))
	rowData := make([]gjson.Result, 0, len(keys))

	for _, key := range keys {
		value := data.Get(key.Str)
		title := key.Str
		rows = append(rows, table.Row{title, formatValue(value, raw)})
		rowData = append(rowData, value)
	}

	// Adjust column widths based on content
	for _, row := range rows {
		for i, cell := range row {
			if i < len(columns) {
				columns[i].Width = max(columns[i].Width, lipgloss.Width(cell))
			}
		}
	}

	t := createTable(columns, rows, objectColor)
	return &TableView{
		path:    path,
		data:    data,
		table:   t,
		rowData: rowData,
		columns: columns,
	}
}

func createTable(columns []table.Column, rows []table.Row, bgColor lipgloss.Color) table.Model {
	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
	)

	// Set common table styles
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(true)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(bgColor).
		Bold(false)
	t.SetStyles(s)

	return t
}

func formatValue(value gjson.Result, raw bool) string {
	if raw {
		return value.Get("@ugly").Raw
	}

	switch {
	case value.IsObject():
		return formatObject(value)
	case value.IsArray():
		return formatArray(value)
	case value.Type == gjson.String:
		return value.Str
	default:
		return value.Raw
	}
}

func formatObject(value gjson.Result) string {
	keys := value.Get("@keys").Array()
	keyStrs := make([]string, len(keys))

	for i, key := range keys {
		val := value.Get(key.Str)
		keyStrs[i] = formatObjectKey(key.Str, val)
	}

	return "{" + strings.Join(keyStrs, ", ") + "}"
}

func formatObjectKey(key string, val gjson.Result) string {
	switch {
	case val.IsObject():
		return key + ":{…}"
	case val.IsArray():
		return key + ":[…]"
	case val.Type == gjson.String:
		str := val.Str
		if lipgloss.Width(str) <= maxPreviewLength {
			return fmt.Sprintf(`%s:"%s"`, key, str)
		}
		return fmt.Sprintf(`%s:"%s…"`, key, truncate.String(str, uint(maxPreviewLength)))
	default:
		return key + ":" + val.Raw
	}
}

func formatArray(value gjson.Result) string {
	switch count := len(value.Array()); count {
	case 0:
		return "[]"
	case 1:
		return "[...1 item...]"
	default:
		return fmt.Sprintf("[...%d items...]", count)
	}
}

func isArrayOfObjects(array []gjson.Result) bool {
	for _, item := range array {
		if !item.IsObject() {
			return false
		}
	}
	return len(array) > 0
}

func sum(ints []int) int {
	total := 0
	for _, n := range ints {
		total += n
	}
	return total
}

// An iterator over `any` values
type AnyIterator interface {
	Next() bool
	Err() error
	Current() any
}

// A generic iterator interface that is used by the `genericIterator` struct
// below to convert iterators over specific types to an AnyIterator
type Iterator[T any] interface {
	Next() bool
	Err() error
	Current() T
}

// genericIterator adapts a generic Iterator[T] to an AnyIterator.
type genericIterator[T any] struct {
	iterator Iterator[T]
	current  any
}

func (g *genericIterator[T]) Next() bool {
	if !g.iterator.Next() {
		return false
	}
	g.current = g.iterator.Current()
	return true
}

func (g *genericIterator[T]) Err() error {
	return g.iterator.Err()
}

func (g *genericIterator[T]) Current() any {
	return g.current
}

func genericToAnyIterator[T any](it Iterator[T]) AnyIterator {
	return &genericIterator[T]{
		iterator: it,
	}
}
