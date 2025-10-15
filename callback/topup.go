package callback

import (
	"database/sql"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"strconv"
	"strings"
	"tbViT/database"
)

func handleTopUpCallback(
	bot *tgbotapi.BotAPI,
	db *sql.DB,
	fromID int64,
	data string,
	callback *tgbotapi.CallbackQuery,
) {
	switch {
	case data == "topup_":
		dep, err := database.GetUserDep(db, fromID)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(fromID, "Ошибка поиска вашего предприятия."))
			return
		}
		database.SendWorkersList(bot, db, fromID, "topup_select_worker", dep, 0)
		answerCallback(bot, callback.ID, "")
		return

	case strings.HasPrefix(data, "topup_amount:"):
		amountStr := strings.TrimPrefix(data, "topup_amount:")
		amount, err := strconv.Atoi(amountStr)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(fromID, "Ошибка суммы пополнения."))
			return
		}
		// Получаем workerID из tmp_field менеджера
		var workerID int64
		err = db.QueryRow(`SELECT tmp_field FROM users WHERE telegram_id=?`, fromID).Scan(&workerID)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(fromID, "Ошибка: работник не выбран."))
			return
		}

		msg, isTrue, _ := database.TopUpBalance(db, workerID, amount)
		bot.Send(tgbotapi.NewMessage(fromID, msg))
		if isTrue {
			bot.Send(tgbotapi.NewMessage(workerID, fmt.Sprintf("Баланс пополнен на %d 🌟!", amount)))
		}
		db.Exec(`UPDATE users SET tmp_field='' WHERE telegram_id=?`, fromID)
		answerCallback(bot, callback.ID, "Готово!")
		return

	case strings.HasPrefix(data, "topup_select_worker:") && (accessLevel == "admin" || accessLevel == "manager"):
		// Проверяем формат callbackData
		// Возможные варианты:
		// - "select_worker:{workerID}"         — выбор работника
		// - "select_worker:{page}:{status}:{dep}" — перелистывание

		parts := strings.Split(data, ":")
		if len(parts) == 2 {
			// 👉 Выбор работника
			workerID, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				bot.Send(tgbotapi.NewMessage(fromID, "Ошибка ID работника."))
				answerCallback(bot, callback.ID, "")
				return
			}
			db.Exec(`UPDATE users SET tmp_field=? WHERE telegram_id=?`, workerID, fromID)
			// Показываем выбор суммы
			replyMarkup := tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("1🌟", "topup_amount:1"),
					tgbotapi.NewInlineKeyboardButtonData("2🌟", "topup_amount:2"),
				),
			)
			info := fmt.Sprintf(database.GetWorkerInfo(db, workerID) + "\nВыберите сумму пополнения:")
			msg := tgbotapi.NewMessage(fromID, info)
			msg.ReplyMarkup = replyMarkup
			bot.Send(msg)
			answerCallback(bot, callback.ID, "")
			return
		} else if len(parts) == 4 {
			// Перелистывание страниц
			// Формат: select_worker:{page}:{status}:{dep}
			page, err := strconv.Atoi(parts[1])
			if err != nil {
				bot.Send(tgbotapi.NewMessage(fromID, "Ошибка страницы."))
				answerCallback(bot, callback.ID, "")
				return
			}
			status := parts[2]
			dep := parts[3]
			err = database.SendWorkersList(bot, db, fromID, status, dep, page)
			if err != nil {
				bot.Send(tgbotapi.NewMessage(fromID, "Ошибка вывода списка работников."))
			}
			answerCallback(bot, callback.ID, "")
			return
		} else {
			// Некорректный формат
			bot.Send(tgbotapi.NewMessage(fromID, "❌ Ошибка при выборе работника."))
			answerCallback(bot, callback.ID, "")
			return
		}
	}

	// Если data не подходит
	bot.Send(tgbotapi.NewMessage(fromID, "❌ Некорректная команда пополнения."))
	answerCallback(bot, callback.ID, "")
}
