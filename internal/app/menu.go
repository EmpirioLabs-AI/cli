package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/EmpirioLabs-AI/cli/internal/agents"
	"github.com/EmpirioLabs-AI/cli/internal/api"
	"github.com/EmpirioLabs-AI/cli/internal/catalog"
	"github.com/EmpirioLabs-AI/cli/internal/config"
)

type agentItem struct {
	agent agents.Agent
	path  string
}

type menuModel struct {
	items    []agentItem
	cursor   int
	keyLabel string
	signedIn bool
	balance  string
	cat      *catalog.Catalog
	model    string // saved default, "" for the catalog's
	status   string

	picking     bool
	models      []catalog.Model
	modelCursor int
	filter      string
	offset      int

	action string // "launch", "login" or ""
	chosen agents.Agent
}

type balanceMsg struct{ text string }
type catalogMsg struct{ cat *catalog.Catalog }

var (
	titleStyle    = lipgloss.NewStyle().Foreground(brand).Bold(true)
	selectedStyle = lipgloss.NewStyle().Foreground(brand).Bold(true)
	mutedStyle    = lipgloss.NewStyle().Foreground(gray)
	goodStyle     = lipgloss.NewStyle().Foreground(green)
)

func (m menuModel) Init() tea.Cmd {
	cmds := []tea.Cmd{func() tea.Msg {
		cat, err := loadCatalog(api.New(Version), false)
		if err != nil {
			return catalogMsg{}
		}
		return catalogMsg{cat}
	}}
	if m.signedIn {
		cmds = append(cmds, func() tea.Msg {
			key, _, err := config.Key()
			if err != nil {
				return balanceMsg{}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			b, err := api.New(Version).Account(ctx, key)
			switch {
			case errors.Is(err, api.ErrInvalidKey):
				return balanceMsg{"key not accepted, press l to sign in again"}
			case err != nil:
				return balanceMsg{"balance unavailable"}
			}
			return balanceMsg{"balance " + money(b.Amount)}
		})
	}
	return tea.Batch(cmds...)
}

func (m menuModel) defaultFor(surface catalog.Surface) (catalog.Model, bool) {
	if m.cat == nil {
		return catalog.Model{}, false
	}
	if m.model != "" {
		if mm, ok := m.cat.Find(m.model); ok && mm.Usable(surface) {
			return mm, true
		}
	}
	return m.cat.Default(surface)
}

const pageSize = 12

func (m menuModel) filtered() []catalog.Model {
	if m.filter == "" {
		return m.models
	}
	var out []catalog.Model
	for _, mm := range m.models {
		if strings.Contains(strings.ToLower(mm.ID+" "+mm.Name()), strings.ToLower(m.filter)) {
			out = append(out, mm)
		}
	}
	return out
}

func (m menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case balanceMsg:
		m.balance = msg.text
		return m, nil
	case catalogMsg:
		m.cat = msg.cat
		return m, nil
	case tea.KeyMsg:
		if m.picking {
			return m.updatePicker(msg)
		}
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "l":
			m.action = "login"
			return m, tea.Quit
		case "m":
			if m.cat == nil {
				m.status = "The model list is still loading."
				return m, nil
			}
			m.picking, m.filter, m.modelCursor, m.offset = true, "", 0, 0
			m.models = m.cat.For(m.items[m.cursor].agent.Surface)
			return m, nil
		case "enter", " ":
			item := m.items[m.cursor]
			if item.path == "" {
				m.status = item.agent.Title + " is not installed. Install it with: " + item.agent.InstallHint()
				return m, nil
			}
			if !m.signedIn {
				m.action, m.chosen = "login", item.agent
				return m, tea.Quit
			}
			m.action, m.chosen = "launch", item.agent
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m menuModel) updatePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	list := m.filtered()
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.picking = false
		return m, nil
	case tea.KeyUp:
		if m.modelCursor > 0 {
			m.modelCursor--
		}
	case tea.KeyDown:
		if m.modelCursor < len(list)-1 {
			m.modelCursor++
		}
	case tea.KeyEnter:
		if len(list) == 0 {
			return m, nil
		}
		chosen := list[m.modelCursor]
		cfg, err := config.Load()
		if err == nil {
			cfg.Model = chosen.ID
			err = config.Save(cfg)
		}
		if err != nil {
			m.status = "Could not save the model: " + err.Error()
		} else {
			m.model = chosen.ID
			m.status = "Default model: " + chosen.Name() + ". Change it any time with m."
		}
		m.picking = false
		return m, nil
	case tea.KeyBackspace:
		if m.filter != "" {
			m.filter = m.filter[:len(m.filter)-1]
			m.modelCursor, m.offset = 0, 0
		}
	case tea.KeyRunes:
		m.filter += string(msg.Runes)
		m.modelCursor, m.offset = 0, 0
	}
	if m.modelCursor < m.offset {
		m.offset = m.modelCursor
	}
	if m.modelCursor >= m.offset+pageSize {
		m.offset = m.modelCursor - pageSize + 1
	}
	return m, nil
}

func (m menuModel) header() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("EmpirioLabs AI") + mutedStyle.Render("  empirio "+Version) + "\n")
	if m.signedIn {
		line := "Signed in " + m.keyLabel
		if m.balance != "" {
			line += mutedStyle.Render("  " + m.balance)
		}
		b.WriteString(line + "\n")
	} else {
		b.WriteString(mutedStyle.Render("Not signed in. Press l to add your API key.") + "\n")
	}
	return b.String()
}

func (m menuModel) View() string {
	if m.picking {
		return m.pickerView()
	}
	var b strings.Builder
	b.WriteString("\n" + m.header())
	if def, ok := m.defaultFor(m.items[m.cursor].agent.Surface); ok {
		b.WriteString(mutedStyle.Render("Model ") + def.Name() + mutedStyle.Render(" ("+def.ID+")") + "\n")
	}
	b.WriteString("\n" + lipgloss.NewStyle().Bold(true).Render("Start an agent") + "\n")
	for i, it := range m.items {
		pointer, title := "  ", it.agent.Title
		if i == m.cursor {
			pointer, title = selectedStyle.Render("> "), selectedStyle.Render(it.agent.Title)
		}
		state := goodStyle.Render("installed")
		if it.path == "" {
			state = mutedStyle.Render("not installed")
		}
		b.WriteString(fmt.Sprintf("%s%s%s%s\n", pointer, title, strings.Repeat(" ", max(1, 16-len(it.agent.Title))), state))
	}
	if m.status != "" {
		b.WriteString("\n" + m.status + "\n")
	}
	b.WriteString("\n" + mutedStyle.Render("up/down choose  enter start  m model  l sign in  q quit") + "\n")
	return b.String()
}

func (m menuModel) pickerView() string {
	var b strings.Builder
	agent := m.items[m.cursor].agent
	b.WriteString("\n" + lipgloss.NewStyle().Bold(true).Render("Default model") + mutedStyle.Render("  (models "+agent.Title+" can run)") + "\n")
	b.WriteString("Search: " + m.filter + "\n\n")
	list := m.filtered()
	end := min(len(list), m.offset+pageSize)
	for i := m.offset; i < end; i++ {
		mm := list[i]
		line := mm.ID
		price := ""
		if p := mm.Price(); p.Known {
			price = fmt.Sprintf("$%s / $%s", agents.Money(p.Input), agents.Money(p.Output))
		}
		ctx := ""
		if mm.ContextWindow > 0 {
			ctx = agents.Tokens(mm.ContextWindow)
		}
		row := fmt.Sprintf("%-30s %-6s %-18s %s", line, ctx, price, mm.Name())
		if i == m.modelCursor {
			b.WriteString(selectedStyle.Render("> "+row) + "\n")
		} else {
			b.WriteString("  " + row + "\n")
		}
	}
	if len(list) == 0 {
		b.WriteString(mutedStyle.Render("  no model matches") + "\n")
	}
	b.WriteString("\n" + mutedStyle.Render(fmt.Sprintf("%d models  type to search  enter set default  esc back  prices per 1M tokens in / out", len(list))) + "\n")
	return b.String()
}

// menu shows the interactive chooser and runs what was picked.
func menu() (int, error) {
	for {
		cfg, _ := config.Load()
		key, _, keyErr := config.Key()
		m := menuModel{signedIn: keyErr == nil, model: cfg.Model}
		if keyErr == nil {
			m.keyLabel = config.Mask(key)
		}
		installedFirst := []agentItem{}
		missing := []agentItem{}
		for _, a := range agents.All() {
			it := agentItem{agent: a, path: a.Find()}
			if it.path != "" {
				installedFirst = append(installedFirst, it)
			} else {
				missing = append(missing, it)
			}
		}
		m.items = append(installedFirst, missing...)
		final, err := tea.NewProgram(m).Run()
		if err != nil {
			return 1, err
		}
		result := final.(menuModel)
		switch result.action {
		case "login":
			if err := login(nil); err != nil {
				failf("%s", err)
			}
			if result.chosen.Name != "" {
				if _, _, err := config.Key(); err == nil {
					return start(result.chosen, launchFlags{}, nil)
				}
			}
			continue
		case "launch":
			return start(result.chosen, launchFlags{}, nil)
		}
		return 0, nil
	}
}
