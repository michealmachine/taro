package notifier

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/michealmachine/taro/internal/db"
)

// TelegramNotifier handles Telegram notifications
type TelegramNotifier struct {
	bot    *tgbotapi.BotAPI
	chatID int64
	logger *slog.Logger
}

// NewTelegramNotifier creates a new Telegram notifier
func NewTelegramNotifier(botToken string, chatID int64, logger *slog.Logger) (*TelegramNotifier, error) {
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	logger.Info("telegram bot initialized", "username", bot.Self.UserName)

	return &TelegramNotifier{
		bot:    bot,
		chatID: chatID,
		logger: logger,
	}, nil
}

// NotifyNewEntry sends a notification when a new entry is added
func (n *TelegramNotifier) NotifyNewEntry(ctx context.Context, entry *db.Entry) {
	text := fmt.Sprintf("📥 *New Entry Added*\n\n"+
		"*Title:* %s\n"+
		"*Type:* %s\n"+
		"*Source:* %s\n"+
		"*Season:* %d\n"+
		"*Status:* %s",
		escapeMarkdown(entry.Title),
		entry.MediaType,
		entry.Source,
		entry.Season,
		entry.Status)

	msg := tgbotapi.NewMessage(n.chatID, text)
	msg.ParseMode = "Markdown"

	if _, err := n.bot.Send(msg); err != nil {
		n.logger.Error("failed to send new entry notification",
			"entry_id", entry.ID,
			"error", err)
	}
}

// NotifyNeedsSelection sends a notification with resource selection options
func (n *TelegramNotifier) NotifyNeedsSelection(ctx context.Context, entry *db.Entry, resources []*db.Resource) {
	text := fmt.Sprintf("🔍 *Resource Selection Needed*\n\n"+
		"*Title:* %s\n"+
		"*Type:* %s\n"+
		"*Season:* %d\n\n"+
		"Found %d resources. Please select one:",
		escapeMarkdown(entry.Title),
		entry.MediaType,
		entry.Season,
		len(resources))

	msg := tgbotapi.NewMessage(n.chatID, text)
	msg.ParseMode = "Markdown"

	// Create inline keyboard with resource options
	var keyboard [][]tgbotapi.InlineKeyboardButton
	for i, resource := range resources {
		sizeGB := 0.0
		if resource.Size.Valid {
			sizeGB = float64(resource.Size.Int64) / 1024 / 1024 / 1024
		}

		buttonText := fmt.Sprintf("%s - %s - %.1f GB",
			resource.Resolution.String,
			resource.Codec.String,
			sizeGB)

		callbackData := fmt.Sprintf("select:%s:%d", entry.ID, i)
		button := tgbotapi.NewInlineKeyboardButtonData(buttonText, callbackData)
		keyboard = append(keyboard, []tgbotapi.InlineKeyboardButton{button})
	}

	// Add cancel button
	cancelButton := tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", fmt.Sprintf("cancel:%s", entry.ID))
	keyboard = append(keyboard, []tgbotapi.InlineKeyboardButton{cancelButton})

	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(keyboard...)

	if _, err := n.bot.Send(msg); err != nil {
		n.logger.Error("failed to send needs selection notification",
			"entry_id", entry.ID,
			"error", err)
	}
}

// NotifyInLibrary sends a notification when an entry is added to library
func (n *TelegramNotifier) NotifyInLibrary(ctx context.Context, entry *db.Entry) {
	text := fmt.Sprintf("✅ *Entry Added to Library*\n\n"+
		"*Title:* %s\n"+
		"*Type:* %s\n"+
		"*Season:* %d\n"+
		"*Path:* %s",
		escapeMarkdown(entry.Title),
		entry.MediaType,
		entry.Season,
		escapeMarkdown(entry.TargetPath.String))

	msg := tgbotapi.NewMessage(n.chatID, text)
	msg.ParseMode = "Markdown"

	if _, err := n.bot.Send(msg); err != nil {
		n.logger.Error("failed to send in library notification",
			"entry_id", entry.ID,
			"error", err)
	}
}

// NotifyFailed sends a notification when an entry fails
func (n *TelegramNotifier) NotifyFailed(ctx context.Context, entry *db.Entry) {
	failureKind := "unknown"
	if entry.FailureKind.Valid {
		failureKind = entry.FailureKind.String
	}

	failureCode := "unknown"
	if entry.FailureCode.Valid {
		failureCode = entry.FailureCode.String
	}

	failedReason := "No details available"
	if entry.FailedReason.Valid {
		failedReason = entry.FailedReason.String
	}

	text := fmt.Sprintf("❌ *Entry Failed*\n\n"+
		"*Title:* %s\n"+
		"*Type:* %s\n"+
		"*Season:* %d\n"+
		"*Stage:* %s\n"+
		"*Failure Kind:* %s\n"+
		"*Failure Code:* %s\n"+
		"*Reason:* %s",
		escapeMarkdown(entry.Title),
		entry.MediaType,
		entry.Season,
		entry.FailedStage.String,
		failureKind,
		failureCode,
		escapeMarkdown(failedReason))

	msg := tgbotapi.NewMessage(n.chatID, text)
	msg.ParseMode = "Markdown"

	// Add retry button only for retryable failures
	if failureKind == "retryable" {
		retryButton := tgbotapi.NewInlineKeyboardButtonData("🔄 Retry", fmt.Sprintf("retry:%s", entry.ID))
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			[]tgbotapi.InlineKeyboardButton{retryButton},
		)
		msg.ReplyMarkup = keyboard
	}

	if _, err := n.bot.Send(msg); err != nil {
		n.logger.Error("failed to send failed notification",
			"entry_id", entry.ID,
			"error", err)
	}
}

// NotifyMountDown sends a notification when OneDrive mount goes down
func (n *TelegramNotifier) NotifyMountDown(ctx context.Context, mountPath string) {
	text := fmt.Sprintf("⚠️ *OneDrive Mount Down*\n\n"+
		"The OneDrive mount is no longer accessible.\n"+
		"*Mount Path:* %s\n\n"+
		"Please check your rclone configuration and mount status.",
		escapeMarkdown(mountPath))

	msg := tgbotapi.NewMessage(n.chatID, text)
	msg.ParseMode = "Markdown"

	if _, err := n.bot.Send(msg); err != nil {
		n.logger.Error("failed to send mount down notification",
			"mount_path", mountPath,
			"error", err)
	}
}

// NotifyMountUp sends a notification when OneDrive mount comes back up
func (n *TelegramNotifier) NotifyMountUp(ctx context.Context, mountPath string) {
	text := fmt.Sprintf("✅ *OneDrive Mount Restored*\n\n"+
		"The OneDrive mount is now accessible again.\n"+
		"*Mount Path:* %s",
		escapeMarkdown(mountPath))

	msg := tgbotapi.NewMessage(n.chatID, text)
	msg.ParseMode = "Markdown"

	if _, err := n.bot.Send(msg); err != nil {
		n.logger.Error("failed to send mount up notification",
			"mount_path", mountPath,
			"error", err)
	}
}

// escapeMarkdown escapes special characters for Telegram Markdown v1
// Markdown v1 only requires escaping: _ * ` [
func escapeMarkdown(text string) string {
	text = strings.ReplaceAll(text, "_", "\\_")
	text = strings.ReplaceAll(text, "*", "\\*")
	text = strings.ReplaceAll(text, "`", "\\`")
	text = strings.ReplaceAll(text, "[", "\\[")
	return text
}

// ParseCallbackData parses callback data from inline keyboard buttons
// Returns action, entryID, and resourceIndex (for select action)
func ParseCallbackData(data string) (action string, entryID string, resourceIndex int, err error) {
	// Format: "action:entry_id" or "action:entry_id:resource_index"
	parts := strings.SplitN(data, ":", 3)

	if len(parts) < 2 {
		return "", "", 0, fmt.Errorf("invalid callback data format: %s", data)
	}

	action = parts[0]
	entryID = parts[1]

	if len(parts) >= 3 {
		resourceIndex, err = strconv.Atoi(parts[2])
		if err != nil {
			return "", "", 0, fmt.Errorf("invalid resource index: %s", parts[2])
		}
	}

	return action, entryID, resourceIndex, nil
}
