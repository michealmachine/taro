package templates

import (
	"strings"
)

type AddEntryPageData struct {
	Meta PageMeta
}

func RenderAddEntryPage(data AddEntryPageData) string {
	var builder strings.Builder
	builder.WriteString("<section class=\"card\">")
	builder.WriteString("<h3>Add entry</h3>")
	builder.WriteString("<form method=\"post\" action=\"/entries\">")
	builder.WriteString("<div class=\"grid grid-3\">" +
		"<label>Title<br><input class=\"button\" name=\"title\" required></label>" +
		"<label>Type<br><select class=\"button\" name=\"media_type\"><option value=\"anime\">anime</option><option value=\"tv\">tv</option><option value=\"movie\">movie</option></select></label>" +
		"<label>Year<br><input class=\"button\" name=\"year\" type=\"number\" min=\"1900\"></label>" +
		"<label>Season<br><input class=\"button\" name=\"season\" type=\"number\" min=\"0\" value=\"1\"></label>" +
		"</div>")
	builder.WriteString("<div class=\"row\" style=\"margin-top:12px\">" +
		"<button class=\"button primary\" type=\"submit\">Create</button>" +
		"</div>")
	builder.WriteString("</form>")
	builder.WriteString("</section>")
	return builder.String()
}

func renderInlineAddEntry() string {
	return RenderAddEntryPage(AddEntryPageData{Meta: PageMeta{Title: "Add"}})
}
