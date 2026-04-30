package templates

import (
	"fmt"
	"html"
	"strings"
	"time"
)

type EntriesPageData struct {
	Meta         PageMeta
	Entries      []EntryListItem
	StatusFilter string
	SourceFilter string
	Now          time.Time
}

type EntryListItem struct {
	ID                 string
	Title              string
	Status             string
	MediaType          string
	Source             string
	Season             int
	Year               int
	UpdatedAt          time.Time
	CreatedAt          time.Time
	FailedStage        string
	FailedReason       string
	SelectedResourceID string
	TargetPath         string
}

func RenderEntriesPage(data EntriesPageData) string {
	var builder strings.Builder

	builder.WriteString("<div class=\"split\">")
	builder.WriteString("<div>")
	builder.WriteString("<h1 class=\"page-title\">Entries</h1>")
	builder.WriteString("<p class=\"subtitle\">All tracked entries with current status and recent activity.</p>")
	builder.WriteString("</div>")
	builder.WriteString("<div class=\"row\">")
	builder.WriteString(renderFilterTag("status", data.StatusFilter))
	builder.WriteString(renderFilterTag("source", data.SourceFilter))
	builder.WriteString("</div>")
	builder.WriteString("</div>")

	builder.WriteString(renderInlineAddEntry())
	builder.WriteString(renderEntriesTable(data.Entries))

	return builder.String()
}

func renderEntriesTable(entries []EntryListItem) string {
	var builder strings.Builder
	builder.WriteString("<section class=\"card\">")
	if len(entries) == 0 {
		builder.WriteString("<div class=\"empty\">No entries found</div>")
		builder.WriteString("</section>")
		return builder.String()
	}

	builder.WriteString("<div class=\"scroll\">")
	builder.WriteString("<table>")
	builder.WriteString("<thead><tr><th>Title</th><th>Status</th><th>Type</th><th>Source</th><th>Updated</th><th>Failure</th></tr></thead>")
	builder.WriteString("<tbody>")
	for _, entry := range entries {
		builder.WriteString("<tr>")
		builder.WriteString("<td><div><a href=\"/entries/" + html.EscapeString(entry.ID) + "\">" + html.EscapeString(entry.Title) + "</a></div>")
		builder.WriteString("<div class=\"muted\">" + html.EscapeString(entry.ID) + "</div></td>")
		builder.WriteString("<td>" + renderStatusBadge(entry.Status) + "</td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(formatType(entry)) + "</td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(entry.Source) + "</td>")
		builder.WriteString("<td class=\"muted nowrap\">" + html.EscapeString(entry.UpdatedAt.Format(time.RFC3339)) + "</td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(formatFailure(entry)) + "</td>")
		builder.WriteString("</tr>")
	}
	builder.WriteString("</tbody></table></div>")
	builder.WriteString("</section>")
	return builder.String()
}

func renderStatusBadge(status string) string {
	class := "info"
	switch status {
	case "in_library", "transferred", "downloaded", "found":
		class = "ok"
	case "failed", "cancelled":
		class = "danger"
	case "needs_selection":
		class = "warn"
	}
	return "<span class=\"tag " + class + "\">" + html.EscapeString(status) + "</span>"
}

func formatType(entry EntryListItem) string {
	label := entry.MediaType
	if entry.MediaType == "movie" && entry.Year > 0 {
		label = fmt.Sprintf("movie (%d)", entry.Year)
	} else if entry.MediaType != "movie" && entry.Season > 0 {
		label = fmt.Sprintf("%s S%02d", entry.MediaType, entry.Season)
	}
	return label
}

func formatFailure(entry EntryListItem) string {
	if entry.FailedStage == "" && entry.FailedReason == "" {
		return "-"
	}
	if entry.FailedReason == "" {
		return entry.FailedStage
	}
	if entry.FailedStage == "" {
		return entry.FailedReason
	}
	return entry.FailedStage + ": " + entry.FailedReason
}

func renderFilterTag(name, value string) string {
	if value == "" {
		return "<span class=\"tag info\">" + html.EscapeString(name) + ": all</span>"
	}
	return "<span class=\"tag info\">" + html.EscapeString(name) + ": " + html.EscapeString(value) + "</span>"
}
