package stepreg

import (
	"database/sql"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log"
)

func RegistrationHandler(bot *tgbotapi.BotAPI, db *sql.DB, update tgbotapi.Update) bool {
	if update.Message == nil || update.Message.From == nil {
		return false
	}
	user := update.Message.From
	userID := user.ID

	var regState, name, tableNumber, restNumber string
	var verified int

	db.QueryRow(
		`SELECT reg_state, name, table_number, rest_number, verified FROM users WHERE telegram_id=?`, userID,
	).Scan(&regState, &name, &tableNumber, &restNumber, &verified)

	// Старт регистрации
	if update.Message.IsCommand() && update.Message.Command() == "start" {
		db.Exec(`INSERT OR IGNORE INTO users (telegram_id, username, verified, reg_state)
                 VALUES (?, ?, 0, 'waiting_name')`, userID, user.UserName)
		db.Exec(`UPDATE users SET reg_state='waiting_name', name='', table_number='', rest_number=''
                 WHERE telegram_id=?`, userID)
		bot.Send(tgbotapi.NewMessage(userID, "Введите ваше имя:"))
		return true
	}

	// Имя
	if regState == "waiting_name" {
		nameInput := update.Message.Text
		db.Exec(`UPDATE users SET name=?, reg_state='waiting_table_number' WHERE telegram_id=?`, nameInput, userID)
		bot.Send(tgbotapi.NewMessage(userID, "Теперь введите номер в расписании:"))
		return true
	}

	// Номер расписания
	if regState == "waiting_table_number" {
		tn := update.Message.Text
		db.Exec(`UPDATE users SET table_number=?, reg_state='waiting_rest_number' WHERE telegram_id=?`, tn, userID)
		bot.Send(tgbotapi.NewMessage(userID, "Введите номер предприятия:"))
		return true
	}

	// Номер предприятия
	if regState == "waiting_rest_number" {
		rn := update.Message.Text

		// Поиск админа ресторана
		var adminTelegramID int64
		err := db.QueryRow(
			`SELECT telegram_id FROM users WHERE rest_number=? AND access_level='admin' LIMIT 1`, rn,
		).Scan(&adminTelegramID)

		if err == sql.ErrNoRows {
			bot.Send(tgbotapi.NewMessage(userID, "❗️ Такого ресторана нет или у него пока нет администратора. Пожалуйста, попробуйте ещё раз!"))
			bot.Send(tgbotapi.NewMessage(userID, "Введите номер предприятия:"))
			return true
		}
		if err != nil {
			bot.Send(tgbotapi.NewMessage(userID, "Произошла ошибка при поиске ресторана. Попробуйте позже!"))
			log.Println("Ошибка поиска администратора:", err)
			return true
		}

		db.Exec(`UPDATE users SET rest_number=?, reg_state='' WHERE telegram_id=?`, rn, userID)
		bot.Send(tgbotapi.NewMessage(userID, "Спасибо, данные переданы на модерацию! Ожидайте подтверждения."))

		// Повторно читаем данные для уведомления админа
		db.QueryRow(`SELECT name, table_number FROM users WHERE telegram_id=?`, userID).Scan(&name, &tableNumber)
		txt := fmt.Sprintf(
			"Новая регистрация!\nИмя: %s\nНомер: %s\nПБО: %s\nUsername: @%s\nTelegram ID: %d",
			name, tableNumber, rn, user.UserName, userID)
		approveKeyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Работник", fmt.Sprintf("approve:worker:%d", userID)),
				tgbotapi.NewInlineKeyboardButtonData("Менеджер", fmt.Sprintf("approve:manager:%d", userID)),
				tgbotapi.NewInlineKeyboardButtonData("Отклонить", fmt.Sprintf("reject:%d", userID)),
			),
		)
		adminMsg := tgbotapi.NewMessage(adminTelegramID, txt)
		adminMsg.ReplyMarkup = approveKeyboard
		bot.Send(adminMsg)
		return true
	}

	return false // если не обрабатывали - можно пройти дальше по коду main
}
