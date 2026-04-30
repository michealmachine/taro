package templates

import (
	"fmt"
	"html"
	"strings"
	"time"
)

type EntryDetailPageData struct {
	Meta      PageMeta
	Entry     EntryDetailView
	Resources []ResourceView
	Logs      []StateLogView
	Now       time.Time
}

type EntryDetailView struct {
	ID                 string
	Title              string
	Status             string
	MediaType          string
	Source             string
	SourceID           string
	Season             int
	Year               int
	AskMode            int
	Resolution         string
	SelectedResourceID string
	SearchStartedAt    time.Time
	DownloadStartedAt  time.Time
	TransferStartedAt  time.Time
	PikPakTaskID       string
	PikPakFileID       string
	PikPakFilePath     string
	TransferTaskID     string
	TargetPath         string
	FailedStage        string
	FailedReason       string
	FailureKind        string
	FailureCode        string
	FailedAt           time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type ResourceView struct {
	ID             string
	Title          string
	Size           int64
	Seeders        int64
	Resolution     string
	Codec          string
	Indexer        string
	Eligible       bool
	Selected       bool
	RejectedReason string
}

func RenderEntryDetailPage(data EntryDetailPageData) string {
	var builder strings.Builder

	builder.WriteString("<div class=\"split\">")
	builder.WriteString("<div>")
	builder.WriteString("<h1 class=\"page-title\">" + html.EscapeString(data.Entry.Title) + "</h1>")
	builder.WriteString("<p class=\"subtitle\">Entry detail view with state transitions and resource context.</p>")
	builder.WriteString("</div>")
	builder.WriteString("<div class=\"row\">")
	builder.WriteString(renderStatusBadge(data.Entry.Status))
	builder.WriteString("</div>")
	builder.WriteString("</div>")

	builder.WriteString(renderEntrySummary(data.Entry))
	builder.WriteString(renderEntryActions(data.Entry))
	builder.WriteString(renderSelectedSummary(data.Entry))
	builder.WriteString(renderEntryTimeline(data.Logs))
	builder.WriteString(renderResources(data.Resources, data.Entry))
	builder.WriteString(renderDownloadBlock(data.Entry))
	builder.WriteString(renderTransferBlock(data.Entry))
	builder.WriteString(renderFailureBlock(data.Entry))

	return builder.String()
}

func renderSelectedSummary(entry EntryDetailView) string {
	if entry.SelectedResourceID == "" {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("<section class=\"card\">")
	builder.WriteString("<h3>Selected resource</h3>")
	builder.WriteString("<div class=\"grid grid-3\">")
	builder.WriteString(renderKpiHTML("Resource ID", "<span class=\"code\">"+html.EscapeString(entry.SelectedResourceID)+"</span>"))
	builder.WriteString(renderKpi("Resolution", safeValue(entry.Resolution)))
	builder.WriteString("</div>")
	builder.WriteString("</section>")
	return builder.String()
}

func renderEntrySummary(entry EntryDetailView) string {
	var builder strings.Builder
	builder.WriteString("<section class=\"card\">")
	builder.WriteString("<h3>Overview</h3>")
	builder.WriteString("<div class=\"grid grid-3\">")
	builder.WriteString(renderKpiHTML("Entry ID", "<span class=\"code\">"+html.EscapeString(entry.ID)+"</span>"))
	builder.WriteString(renderKpi("Source", html.EscapeString(entry.Source)))
	builder.WriteString(renderKpiHTML("Source ID", "<span class=\"code\">"+html.EscapeString(entry.SourceID)+"</span>"))
	builder.WriteString(renderKpi("Media", html.EscapeString(formatEntryType(entry))))
	builder.WriteString(renderKpi("Ask mode", fmt.Sprintf("%d", entry.AskMode)))
	builder.WriteString(renderKpi("Resolution", safeValue(entry.Resolution)))
	builder.WriteString(renderKpi("Created", entry.CreatedAt.Format(time.RFC3339)))
	builder.WriteString(renderKpi("Updated", entry.UpdatedAt.Format(time.RFC3339)))
	builder.WriteString(renderKpiHTML("Status", renderStatusBadge(entry.Status)))
	builder.WriteString("</div>")
	builder.WriteString("</section>")
	return builder.String()
}

func renderEntryActions(entry EntryDetailView) string {
	var builder strings.Builder
	builder.WriteString("<section class=\"card\">")
	builder.WriteString("<h3>Actions</h3>")
	builder.WriteString("<div class=\"row\">" +
		"<form method=\"post\" action=\"/entries/" + html.EscapeString(entry.ID) + "/retry\"><button class=\"button primary\" type=\"submit\">Retry</button></form>" +
		"<form method=\"post\" action=\"/entries/" + html.EscapeString(entry.ID) + "/cancel\"><button class=\"button danger\" type=\"submit\">Cancel</button></form>" +
		"</div>")
	builder.WriteString("</section>")
	return builder.String()
}

func renderEntryTimeline(logs []StateLogView) string {
	var builder strings.Builder
	builder.WriteString("<section class=\"card\">")
	builder.WriteString("<h3>State timeline</h3>")
	if len(logs) == 0 {
		builder.WriteString("<div class=\"empty\">No state transitions recorded</div>")
		builder.WriteString("</section>")
		return builder.String()
	}
	builder.WriteString("<div class=\"scroll\"><table>")
	builder.WriteString("<thead><tr><th>From</th><th>To</th><th>Reason</th><th>At</th></tr></thead><tbody>")
	for _, log := range logs {
		builder.WriteString("<tr>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(log.FromStatus) + "</td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(log.ToStatus) + "</td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(log.Reason) + "</td>")
		builder.WriteString("<td class=\"muted\">" + log.CreatedAt.Format(time.RFC3339) + "</td>")
		builder.WriteString("</tr>")
	}
	builder.WriteString("</tbody></table></div></section>")
	return builder.String()
}

func renderResources(resources []ResourceView, entry EntryDetailView) string {
	var builder strings.Builder
	builder.WriteString("<section class=\"card\">")
	builder.WriteString("<h3>Resources</h3>")
	if len(resources) == 0 {
		builder.WriteString("<div class=\"empty\">No resources recorded</div>")
		builder.WriteString("</section>")
		return builder.String()
	}
	builder.WriteString("<div class=\"scroll\"><table>")
	builder.WriteString("<thead><tr><th>Title</th><th>Resolution</th><th>Codec</th><th>Seeders</th><th>Eligible</th><th>Action</th></tr></thead><tbody>")
	for _, resource := range resources {
		builder.WriteString("<tr>")
		builder.WriteString("<td><div>" + html.EscapeString(resource.Title) + "</div><div class=\"muted\">" + html.EscapeString(resource.Indexer) + "</div></td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(resource.Resolution) + "</td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(resource.Codec) + "</td>")
		builder.WriteString("<td class=\"muted\">" + formatNumber(resource.Seeders) + "</td>")
		builder.WriteString("<td>" + renderEligibility(resource) + "</td>")
		builder.WriteString("<td>" + renderSelectButton(entry, resource) + "</td>")
		builder.WriteString("</tr>")
	}
	builder.WriteString("</tbody></table></div></section>")
	return builder.String()
}

func renderDownloadBlock(entry EntryDetailView) string {
	var builder strings.Builder
	builder.WriteString("<section class=\"card\">")
	builder.WriteString("<h3>Download</h3>")
	builder.WriteString("<div class=\"grid grid-3\">")
	builder.WriteString(renderKpi("PikPak task", safeValue(entry.PikPakTaskID)))
	builder.WriteString(renderKpi("File ID", safeValue(entry.PikPakFileID)))
	builder.WriteString(renderKpiHTML("File path", "<span class=\"code\">"+html.EscapeString(safeValue(entry.PikPakFilePath))+"</span>"))
	builder.WriteString(renderKpi("Download started", formatTime(entry.DownloadStartedAt, time.Time{})))
	builder.WriteString(renderKpi("Search started", formatTime(entry.SearchStartedAt, time.Time{})))
	builder.WriteString("</div>")
	builder.WriteString("</section>")
	return builder.String()
}

func renderTransferBlock(entry EntryDetailView) string {
	var builder strings.Builder
	builder.WriteString("<section class=\"card\">")
	builder.WriteString("<h3>Transfer</h3>")
	builder.WriteString("<div class=\"grid grid-3\">")
	builder.WriteString(renderKpi("Transfer task", safeValue(entry.TransferTaskID)))
	builder.WriteString(renderKpiHTML("Target path", "<span class=\"code\">"+html.EscapeString(safeValue(entry.TargetPath))+"</span>"))
	builder.WriteString(renderKpi("Transfer started", formatTime(entry.TransferStartedAt, time.Time{})))
	builder.WriteString("</div>")
	builder.WriteString("</section>")
	return builder.String()
}

func renderFailureBlock(entry EntryDetailView) string {
	var builder strings.Builder
	builder.WriteString("<section class=\"card\">")
	builder.WriteString("<h3>Failure</h3>")
	builder.WriteString("<div class=\"grid grid-3\">")
	builder.WriteString(renderKpi("Stage", safeValue(entry.FailedStage)))
	builder.WriteString(renderKpi("Reason", safeValue(entry.FailedReason)))
	builder.WriteString(renderKpi("Kind", safeValue(entry.FailureKind)))
	builder.WriteString(renderKpi("Code", safeValue(entry.FailureCode)))
	builder.WriteString(renderKpi("Failed at", formatTime(entry.FailedAt, time.Time{})))
	builder.WriteString("</div>")
	builder.WriteString("</section>")
	return builder.String()
}

func formatEntryType(entry EntryDetailView) string {
	if entry.MediaType == "movie" && entry.Year > 0 {
		return fmt.Sprintf("movie (%d)", entry.Year)
	}
	if entry.MediaType != "movie" && entry.Season > 0 {
		return fmt.Sprintf("%s S%02d", entry.MediaType, entry.Season)
	}
	return entry.MediaType
}

func renderEligibility(resource ResourceView) string {
	if resource.Selected {
		return "<span class=\"tag ok\">selected</span>"
	}
	if resource.Eligible {
		return "<span class=\"tag info\">eligible</span>"
	}
	label := "filtered"
	if resource.RejectedReason != "" {
		label += ": " + resource.RejectedReason
	}
	return "<span class=\"tag danger\">" + html.EscapeString(label) + "</span>"
}

func renderSelectButton(entry EntryDetailView, resource ResourceView) string {
	if entry.Status != "needs_selection" || !resource.Eligible {
		return "-"
	}
	return "<form method=\"post\" action=\"/entries/" + html.EscapeString(entry.ID) + "/select\">" +
		"<input type=\"hidden\" name=\"resource_id\" value=\"" + html.EscapeString(resource.ID) + "\">" +
		"<button class=\"button primary\" type=\"submit\">Select</button>" +
		"</form>"
}

func formatNumber(value int64) string {
	if value <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d", value)
}

func safeValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
