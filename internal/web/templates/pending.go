package templates

import (
	"html"
	"strings"
)

type PendingPageData struct {
	Meta    PageMeta
	Entries []PendingEntryView
}

type PendingEntryView struct {
	ID        string
	Title     string
	MediaType string
	Source    string
	Resources []ResourceView
}

func RenderPendingPage(data PendingPageData) string {
	var builder strings.Builder

	builder.WriteString("<div class=\"split\">")
	builder.WriteString("<div>")
	builder.WriteString("<h1 class=\"page-title\">Needs selection</h1>")
	builder.WriteString("<p class=\"subtitle\">Entries waiting for a resource decision.</p>")
	builder.WriteString("</div>")
	builder.WriteString("</div>")

	if len(data.Entries) == 0 {
		builder.WriteString("<section class=\"card\"><div class=\"empty\">No pending entries</div></section>")
		return builder.String()
	}

	for _, entry := range data.Entries {
		builder.WriteString("<section class=\"card\">")
		builder.WriteString("<div class=\"split\">")
		builder.WriteString("<div>")
		builder.WriteString("<h3><a href=\"/entries/" + html.EscapeString(entry.ID) + "\">" + html.EscapeString(entry.Title) + "</a></h3>")
		builder.WriteString("<div class=\"muted\">" + html.EscapeString(entry.MediaType) + " · " + html.EscapeString(entry.Source) + "</div>")
		builder.WriteString("</div>")
		builder.WriteString("<span class=\"tag warn\">needs_selection</span>")
		builder.WriteString("</div>")

		if len(entry.Resources) == 0 {
			builder.WriteString("<div class=\"empty\">No resources available</div>")
			builder.WriteString("</section>")
			continue
		}

		builder.WriteString("<div class=\"scroll\"><table>")
		builder.WriteString("<thead><tr><th>Title</th><th>Resolution</th><th>Codec</th><th>Seeders</th><th>Eligible</th><th>Action</th></tr></thead><tbody>")
		for _, resource := range entry.Resources {
			builder.WriteString("<tr>")
			builder.WriteString("<td><div>" + html.EscapeString(resource.Title) + "</div><div class=\"muted\">" + html.EscapeString(resource.Indexer) + "</div></td>")
			builder.WriteString("<td class=\"muted\">" + html.EscapeString(resource.Resolution) + "</td>")
			builder.WriteString("<td class=\"muted\">" + html.EscapeString(resource.Codec) + "</td>")
			builder.WriteString("<td class=\"muted\">" + formatNumber(resource.Seeders) + "</td>")
			builder.WriteString("<td>" + renderEligibility(resource) + "</td>")
			builder.WriteString("<td>")
			if resource.Eligible {
				builder.WriteString("<form method=\"post\" action=\"/entries/" + html.EscapeString(entry.ID) + "/select\">" +
					"<input type=\"hidden\" name=\"resource_id\" value=\"" + html.EscapeString(resource.ID) + "\">" +
					"<button class=\"button primary\" type=\"submit\">Select</button>" +
					"</form>")
			} else {
				builder.WriteString("-")
			}
			builder.WriteString("</td>")
			builder.WriteString("</tr>")
		}
		builder.WriteString("</tbody></table></div>")
		builder.WriteString("</section>")
	}

	return builder.String()
}
