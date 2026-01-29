package callback

import (
	"database/sql"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log"
	"strings"
	"tbViT/database"
)

func handleHistoryTopUp(db *sql.DB, cq *tgbotapi.CallbackQuery, fromID int64, data string, bot *tgbotapi.BotAPI) {
	switch {
	// --- Получение истории пополнений ---
	case data == "history_topup":
		_, restNum, _, _, _, err := database.GetRegInfo(db, fromID)
		if err != nil {
			log.Printf("Ошибка получения информации о регистрации для %d: %v", fromID, err)
			bot.Send(tgbotapi.NewMessage(fromID, "❌ Ошибка получения информации о вашем предприятии."))
			answerCallback(bot, cq.ID, "Ошибка.") // Добавим ответ на callback
			return
		}

		historySlice, err := database.GetTopUpHistory(db, restNum, 30) // Переименовал в historySlice
		if err != nil {
			log.Printf("Ошибка получения истории пополнений для ресторана %d: %v", restNum, err)
			bot.Send(tgbotapi.NewMessage(fromID, "❌ Ошибка получения истории пополнений."))
			answerCallback(bot, cq.ID, "Ошибка.") // Добавим ответ на callback
			return
		}

		var responseMessage string
		if len(historySlice) == 0 {
			responseMessage = "📜 История пополнений пуста."
		} else {
			// Объединяем слайс строк в одну строку
			responseMessage = "📜 **Последние пополнения баланса:**\n\n" + strings.Join(historySlice, "\n")
		}

		// Дополнительная проверка на длину сообщения для Telegram (на всякий случай)
		const MAX_TG_MESSAGE_LENGTH = 4096
		if len(responseMessage) > MAX_TG_MESSAGE_LENGTH {
			responseMessage = responseMessage[:MAX_TG_MESSAGE_LENGTH-200] + "\n\n... (Полная история слишком длинная, показана часть)."
		}

		msg := tgbotapi.NewMessage(fromID, responseMessage)
		msg.ParseMode = tgbotapi.ModeMarkdown
		if _, err := bot.Send(msg); err != nil {
			log.Printf("❌ Ошибка отправки сообщения с историей пополнений для %d: %v", fromID, err)
			bot.Send(tgbotapi.NewMessage(fromID, "❌ Не удалось отправить историю пополнений.")) // Сообщить пользователю
		}

		answerCallback(bot, cq.ID, "История пополнений получена.") // Уведомляем Telegram, что callback обработан
		return
	}
}
