package templates

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"time"
)

type StatusPageData struct {
	Meta PageMeta

	Uptime           time.Duration
	StartTime        time.Time
	StatusCounts     map[string]int
	RecentLogs       []StateLogView
	RecentFailed     []EntrySummary
	ActiveTransfers  []TransferSummary
	PendingSelection []EntrySummary
	OneDriveHealthy  *bool
	TaskRecords      []TaskRecordView
	TransferPollTime time.Time
	DownloadPollTime time.Time
}

type EntrySummary struct {
	ID           string
	Title        string
	Status       string
	MediaType    string
	Source       string
	UpdatedAt    time.Time
	FailedStage  string
	FailedReason string
}

type TransferSummary struct {
	EntryID       string
	Title         string
	TaskID        string
	Status        string
	Error         string
	StartedAt     time.Time
	LastCheckedAt time.Time
	TaskCreatedAt time.Time
	TaskUpdatedAt time.Time
	Elapsed       time.Duration
	TargetPath    string
}

type StateLogView struct {
	EntryID    string
	FromStatus string
	ToStatus   string
	Reason     string
	CreatedAt  time.Time
}

type TaskRecordView struct {
	Name         string
	LastStarted  time.Time
	LastFinished time.Time
	LastDuration time.Duration
	LastError    string
}

func RenderStatusPage(data StatusPageData) string {
	var builder strings.Builder

	builder.WriteString("<div class=\"split\">")
	builder.WriteString("<div>")
	builder.WriteString("<h1 class=\"page-title\">System status</h1>")
	builder.WriteString("<p class=\"subtitle\">Live view of schedulers, queues, and transfer tracking.</p>")
	builder.WriteString("</div>")
	builder.WriteString("<div class=\"row\">")
	builder.WriteString("<span class=\"pill\">Up " + html.EscapeString(formatDuration(data.Uptime)) + "</span>")
	builder.WriteString("</div>")
	builder.WriteString("</div>")

	builder.WriteString(renderStatusOverview(data))
	builder.WriteString(renderStatusCounts(data.StatusCounts))
	builder.WriteString(renderTransferPanel(data.ActiveTransfers))
	builder.WriteString(renderPendingPanel(data.PendingSelection))
	builder.WriteString(renderRecentFailed(data.RecentFailed))
	builder.WriteString(renderTaskRecords(data.TaskRecords, data.TransferPollTime, data.DownloadPollTime))
	builder.WriteString(renderRecentLogs(data.RecentLogs))

	return builder.String()
}

func renderStatusOverview(data StatusPageData) string {
	var builder strings.Builder
	statusTag := "<span class=\"tag info\">unknown</span>"
	if data.OneDriveHealthy != nil {
		if *data.OneDriveHealthy {
			statusTag = "<span class=\"tag ok\">healthy</span>"
		} else {
			statusTag = "<span class=\"tag danger\">down</span>"
		}
	}

	builder.WriteString("<section class=\"card\">")
	builder.WriteString("<h3>Runtime</h3>")
	builder.WriteString("<div class=\"grid grid-3\">")
	builder.WriteString(renderKpi("Started", data.StartTime.Format(time.RFC3339)))
	builder.WriteString(renderKpi("Uptime", formatDuration(data.Uptime)))
	builder.WriteString(renderKpiHTML("OneDrive", statusTag))
	builder.WriteString("</div>")
	builder.WriteString("</section>")

	return builder.String()
}

func renderStatusCounts(counts map[string]int) string {
	var builder strings.Builder
	statuses := []string{"pending", "searching", "found", "downloading", "downloaded", "transferring", "transferred", "in_library", "needs_selection", "failed", "cancelled"}

	builder.WriteString("<section class=\"card\">")
	builder.WriteString("<h3>Entry counts</h3>")
	builder.WriteString("<div class=\"grid grid-3\">")
	for _, status := range statuses {
		count := counts[status]
		builder.WriteString(renderKpi(strings.ReplaceAll(status, "_", " "), fmt.Sprintf("%d", count)))
	}
	builder.WriteString("</div>")
	builder.WriteString("</section>")

	return builder.String()
}

func renderTransferPanel(transfers []TransferSummary) string {
	var builder strings.Builder
	builder.WriteString("<section class=\"card\">")
	builder.WriteString("<h3>Active transfers</h3>")
	if len(transfers) == 0 {
		builder.WriteString("<div class=\"empty\">No active transfers</div>")
		builder.WriteString("</section>")
		return builder.String()
	}

	builder.WriteString("<div class=\"scroll\">")
	builder.WriteString("<table>")
	builder.WriteString("<thead><tr><th>Entry</th><th>Status</th><th>Elapsed</th><th>Last update</th><th>Target</th></tr></thead>")
	builder.WriteString("<tbody>")
	for _, item := range transfers {
		builder.WriteString("<tr>")
		builder.WriteString("<td><div><a href=\"/entries/" + html.EscapeString(item.EntryID) + "\">" + html.EscapeString(item.Title) + "</a></div><div class=\"muted\">" + html.EscapeString(item.TaskID) + "</div></td>")
		builder.WriteString("<td>" + formatStatusTag(item.Status, item.Error) + "</td>")
		builder.WriteString("<td class=\"nowrap\">" + html.EscapeString(formatDuration(item.Elapsed)) + "</td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(formatTime(item.TaskUpdatedAt, item.LastCheckedAt)) + "</td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(item.TargetPath) + "</td>")
		builder.WriteString("</tr>")
	}
	builder.WriteString("</tbody></table></div>")
	builder.WriteString("</section>")
	return builder.String()
}

func renderPendingPanel(entries []EntrySummary) string {
	var builder strings.Builder
	builder.WriteString("<section class=\"card\">")
	builder.WriteString("<h3>Needs selection</h3>")
	if len(entries) == 0 {
		builder.WriteString("<div class=\"empty\">No entries waiting for selection</div>")
		builder.WriteString("</section>")
		return builder.String()
	}

	builder.WriteString("<div class=\"list\">")
	for _, entry := range entries {
		builder.WriteString("<div class=\"split\">")
		builder.WriteString("<div><a href=\"/entries/" + html.EscapeString(entry.ID) + "\">" + html.EscapeString(entry.Title) + "</a><div class=\"muted\">" + html.EscapeString(entry.MediaType) + " · " + html.EscapeString(entry.Source) + "</div></div>")
		builder.WriteString("<span class=\"tag warn\">" + html.EscapeString(entry.Status) + "</span>")
		builder.WriteString("</div>")
	}
	builder.WriteString("</div>")
	builder.WriteString("</section>")

	return builder.String()
}

func renderRecentFailed(entries []EntrySummary) string {
	var builder strings.Builder
	builder.WriteString("<section class=\"card\">")
	builder.WriteString("<h3>Recent failures</h3>")
	if len(entries) == 0 {
		builder.WriteString("<div class=\"empty\">No recent failures</div>")
		builder.WriteString("</section>")
		return builder.String()
	}

	builder.WriteString("<div class=\"scroll\">")
	builder.WriteString("<table>")
	builder.WriteString("<thead><tr><th>Entry</th><th>Stage</th><th>Reason</th><th>Updated</th></tr></thead>")
	builder.WriteString("<tbody>")
	for _, entry := range entries {
		builder.WriteString("<tr>")
		builder.WriteString("<td><a href=\"/entries/" + html.EscapeString(entry.ID) + "\">" + html.EscapeString(entry.Title) + "</a></td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(entry.FailedStage) + "</td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(entry.FailedReason) + "</td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(entry.UpdatedAt.Format(time.RFC3339)) + "</td>")
		builder.WriteString("</tr>")
	}
	builder.WriteString("</tbody></table></div>")
	builder.WriteString("</section>")
	return builder.String()
}

func renderTaskRecords(records []TaskRecordView, transferPollTime time.Time, downloadPollTime time.Time) string {
	var builder strings.Builder
	builder.WriteString("<section class=\"card\">")
	builder.WriteString("<h3>Scheduler activity</h3>")
	if len(records) == 0 && transferPollTime.IsZero() && downloadPollTime.IsZero() {
		builder.WriteString("<div class=\"empty\">No scheduler activity recorded yet</div>")
		builder.WriteString("</section>")
		return builder.String()
	}

	if len(records) > 1 {
		sort.Slice(records, func(i, j int) bool { return records[i].Name < records[j].Name })
	}
	builder.WriteString("<div class=\"scroll\">")
	builder.WriteString("<table>")
	builder.WriteString("<thead><tr><th>Task</th><th>Last start</th><th>Last finish</th><th>Duration</th><th>Error</th></tr></thead>")
	builder.WriteString("<tbody>")
	for _, record := range records {
		builder.WriteString("<tr>")
		builder.WriteString("<td class=\"code\">" + html.EscapeString(record.Name) + "</td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(formatTime(record.LastStarted, time.Time{})) + "</td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(formatTime(record.LastFinished, time.Time{})) + "</td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(formatDuration(record.LastDuration)) + "</td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(record.LastError) + "</td>")
		builder.WriteString("</tr>")
	}
	if !transferPollTime.IsZero() || !downloadPollTime.IsZero() {
		builder.WriteString("<tr>")
		builder.WriteString("<td class=\"code\">polling</td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(formatTime(downloadPollTime, time.Time{})) + "</td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(formatTime(transferPollTime, time.Time{})) + "</td>")
		builder.WriteString("<td class=\"muted\">-</td>")
		builder.WriteString("<td class=\"muted\">downloader/transfer</td>")
		builder.WriteString("</tr>")
	}
	builder.WriteString("</tbody></table></div>")
	builder.WriteString("</section>")

	return builder.String()
}

func renderRecentLogs(logs []StateLogView) string {
	var builder strings.Builder
	builder.WriteString("<section class=\"card\">")
	builder.WriteString("<h3>Recent state transitions</h3>")
	if len(logs) == 0 {
		builder.WriteString("<div class=\"empty\">No state transitions recorded yet</div>")
		builder.WriteString("</section>")
		return builder.String()
	}

	builder.WriteString("<div class=\"scroll\">")
	builder.WriteString("<table>")
	builder.WriteString("<thead><tr><th>Entry</th><th>From</th><th>To</th><th>Reason</th><th>At</th></tr></thead>")
	builder.WriteString("<tbody>")
	for _, log := range logs {
		builder.WriteString("<tr>")
		builder.WriteString("<td><a href=\"/entries/" + html.EscapeString(log.EntryID) + "\">" + html.EscapeString(log.EntryID) + "</a></td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(log.FromStatus) + "</td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(log.ToStatus) + "</td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(log.Reason) + "</td>")
		builder.WriteString("<td class=\"muted\">" + html.EscapeString(log.CreatedAt.Format(time.RFC3339)) + "</td>")
		builder.WriteString("</tr>")
	}
	builder.WriteString("</tbody></table></div>")
	builder.WriteString("</section>")
	return builder.String()
}

func renderKpi(label, value string) string {
	return "<div class=\"kpi\"><span>" + html.EscapeString(label) + "</span><strong>" + html.EscapeString(value) + "</strong></div>"
}

func renderKpiHTML(label, valueHTML string) string {
	return "<div class=\"kpi\"><span>" + html.EscapeString(label) + "</span><strong>" + valueHTML + "</strong></div>"
}

func formatDuration(duration time.Duration) string {
	if duration <= 0 {
		return "0s"
	}
	hours := int(duration.Hours())
	minutes := int(duration.Minutes()) % 60
	seconds := int(duration.Seconds()) % 60
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func formatStatusTag(status, err string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return "<span class=\"tag info\">unknown</span>"
	}
	class := "info"
	if status == "done" {
		class = "ok"
	} else if status == "failed" {
		class = "danger"
	} else if status == "running" || status == "pending" {
		class = "warn"
	}
	label := html.EscapeString(status)
	if err != "" {
		label += " · error"
	}
	return "<span class=\"tag " + class + "\">" + label + "</span>"
}

func formatTime(primary time.Time, fallback time.Time) string {
	if !primary.IsZero() {
		return primary.Format(time.RFC3339)
	}
	if !fallback.IsZero() {
		return fallback.Format(time.RFC3339)
	}
	return "-"
}
