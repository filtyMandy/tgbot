package callback

import (
	"database/sql"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func handleSuper(bot *tgbotapi.BotAPI, db *sql.DB, cq *tgbotapi.CallbackQuery, userState map[int64]*CorrectionState) {
	data := cq.Data
	fromID := cq.From.ID

	switch data {
	case "super_user:transition":
		userState[fromID] = &CorrectionState{
			ID:    fromID,
			Field: "super_user:wait_rest_number",
		}
		msg := tgbotapi.NewMessage(fromID, "Номер предприятия:")
		bot.Send(msg)

	case "super_user:access":
		userState[fromID] = &CorrectionState{
			ID:    fromID,
			Field: "super_user:wait_access_level",
		}
		msg := tgbotapi.NewMessage(fromID, "Уровень доступа(worker/manager/admin):")
		bot.Send(msg)
	}
}
