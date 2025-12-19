package callback

import (
	"database/sql"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log"
	"strconv"
	"strings"
	"tbViT/database"
	"tbViT/features"
)

type CorrectionState struct {
	ID    int64
	Field string // // параметр для корректировки
	Value string
}

var accessLevel string

func HandleCallback(bot *tgbotapi.BotAPI, db *sql.DB, callback *tgbotapi.CallbackQuery, userState map[int64]*CorrectionState, shopState map[int64]*CorrectionState, superUser int64) {
	fromID := callback.From.ID
	data := callback.Data

	adminTelegramID, err := database.GetAdminID(db, fromID)
	if err != nil {
		log.Println(err)
	}

	accessLevel, err = database.GetAccessLevel(db, fromID)
	if err != nil {
		log.Println(err)
	}

	log.Printf("Callback data: %s, user: %d, level: %s", data, fromID, accessLevel)

	switch {
	case strings.HasPrefix(data, "super_user") && fromID == superUser:
		handleSuper(bot, db, callback, userState)
		answerCallback(bot, callback.ID, "")

	case strings.HasPrefix(data, "approve:") && accessLevel == "admin":
		parts := strings.Split(data, ":")
		if len(parts) == 3 {
			role := parts[1]
			uid, _ := strconv.ParseInt(parts[2], 10, 64)
			db.Exec(`UPDATE users SET access_level=?, verified=1, current_balance=0, last_ts=0 WHERE telegram_id=?`, role, uid)
			bot.Send(tgbotapi.NewMessage(uid, fmt.Sprintf("✅ Регистрация подтверждена! Ваш статус: %s.\n/menu — доступ к функциям.", role)))
			answerCallback(bot, callback.ID, "Пользователь принят.")
			return
		}

	case strings.HasPrefix(data, "reject:") && accessLevel == "admin":
		parts := strings.Split(data, ":")
		if len(parts) == 2 {
			uid, _ := strconv.ParseInt(parts[1], 10, 64)
			db.Exec(`UPDATE users SET verified=0, access_level='' WHERE telegram_id=?`, uid)
			bot.Send(tgbotapi.NewMessage(uid, "❌ Ваша регистрация отклонена администратором."))
			bot.Send(tgbotapi.NewMessage(adminTelegramID, "Пользователь отклонён."))
			answerCallback(bot, callback.ID, "Заявка отклонена")
			return
		}

	case data == "menu_admin_setbal" && accessLevel == "admin":
		dep, err := database.GetUserDep(db, fromID)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(fromID, "Ошибка поиска вашего предприятия."))
			return
		}
		database.SendWorkersList(bot, db, fromID, "correction", dep, 0)
		answerCallback(bot, callback.ID, "")
		return

	case strings.HasPrefix(data, "correction:") && accessLevel == "admin":
		parts := strings.Split(data, ":")
		if len(parts) != 2 {
			return
		}
		workerID, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return
		}

		// Сохраняем workerID, но поле пока пустое
		userState[fromID] = &CorrectionState{ID: workerID}
		info := database.GetWorkerInfo(db, workerID)
		message := fmt.Sprintf("%s\nЧто хотите скорректировать?", info)
		msg := tgbotapi.NewMessage(fromID, message)
		// Инлайн-клавиатура с выбором параметра
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Баланс", "setfield:balance"),
				tgbotapi.NewInlineKeyboardButtonData("Имя", "setfield:name"),
				tgbotapi.NewInlineKeyboardButtonData("Номер", "setfield:tablenumber"),
				tgbotapi.NewInlineKeyboardButtonData("❗️Удалить❗️", "setfield:delete"),
			),
		)
		bot.Send(msg)
		answerCallback(bot, callback.ID, "")
		return

	case data == "accesslevel" && accessLevel == "admin":
		roleMarkup := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Работник", "changeRole:worker"),
				tgbotapi.NewInlineKeyboardButtonData("Менеджер", "changeRole:manager"),
				tgbotapi.NewInlineKeyboardButtonData("Админ", "changeRole:admin"),
			),
		)
		msg := tgbotapi.NewMessage(fromID, "Выберите роль, которую хотите передать:")
		msg.ReplyMarkup = roleMarkup
		bot.Send(msg)

	case strings.HasPrefix(data, "changeRole:") && accessLevel == "admin":
		role := strings.TrimPrefix(data, "changeRole:")
		userState[fromID] = &CorrectionState{
			Field: "wait_table_number",
			Value: role,
		}
		bot.Send(tgbotapi.NewMessage(fromID, "Введите номер расписания работника:"))
		answerCallback(bot, callback.ID, "")
		return

	case strings.HasPrefix(data, "confirmAdmin:") && accessLevel == "admin":
		tableNumber := strings.TrimPrefix(data, "confirmAdmin:")
		_, ok := userState[fromID]
		if !ok {
			bot.Send(tgbotapi.NewMessage(fromID, "⛔ Ошибка действия. Попробуйте начать заново."))
			answerCallback(bot, callback.ID, "")
			return
		}
		role := "admin"
		err := database.ChangeRole(db, fromID, tableNumber, role)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(fromID, "❌ Ошибка назначения админа: "+err.Error()))
		} else {
			bot.Send(tgbotapi.NewMessage(fromID, "✅ Теперь этот человек — админ. Вы стали менеджером."))
		}
		delete(userState, fromID)
		answerCallback(bot, callback.ID, "")
		return

	case data == "cancelAdmin" && accessLevel == "admin":
		bot.Send(tgbotapi.NewMessage(fromID, "✅ Операция отменена!"))
		delete(userState, fromID)
		answerCallback(bot, callback.ID, "")
		return

	case strings.HasPrefix(data, "setfield:") && accessLevel == "admin":
		parts := strings.Split(data, ":")
		if len(parts) != 2 {
			return
		}
		field := parts[1]
		state, ok := userState[fromID]
		if !ok {
			return
		}
		if field == "delete" {
			// Удаляем ПОЛЬЗОВАТЕЛЯ, ID которого хранится в state.ID
			err := database.DeleteUser(db, state.ID) // ← передаём db и workerID
			if err != nil {
				log.Printf("Ошибка удаления пользователя %d: %v", state.ID, err)
				bot.Send(tgbotapi.NewMessage(fromID, "❌ Не удалось удалить пользователя."))
			} else {
				bot.Send(tgbotapi.NewMessage(fromID, "✅ Пользователь удалён."))
			}
			delete(userState, fromID)
			answerCallback(bot, callback.ID, "")
			return
		}

		messege := fmt.Sprintf("Введите новое значение(%s):", field)
		state.Field = field // теперь помним и работника, и поле
		bot.Send(tgbotapi.NewMessage(fromID, messege))
		answerCallback(bot, callback.ID, "")
		return

	case strings.HasPrefix(data, "shop_edit") && accessLevel == "admin":
		handleShopEdit(bot, db, callback, shopState)
		answerCallback(bot, callback.ID, "")
		return

	case strings.HasPrefix(data, "orders") && accessLevel == "admin":
		handleOrderComplete(bot, db, callback)
		answerCallback(bot, callback.ID, "")
		return

	case strings.HasPrefix(data, "topup_") && (accessLevel == "admin" || accessLevel == "manager"):
		handleTopUpCallback(bot, db, fromID, data, callback)
		answerCallback(bot, callback.ID, "")
		return

	case data == "menu_list" && (accessLevel == "admin" || accessLevel == "manager"):
		list, err := database.SendWorkersString(db, fromID)
		msg := fmt.Sprintf("Актуальный список сотрудников:\n%s", list)
		if err != nil {
			msg = "Ошибка загрузки списка"
		}
		bot.Send(tgbotapi.NewMessage(fromID, msg))
		answerCallback(bot, callback.ID, "")
		return

	case data == "show_balance" && accessLevel == "worker":
		balance, err := database.GetBalance(db, fromID)
		msg := ""
		if err != nil {
			msg = "Ошибка получения баланса!"
		} else {
			msg = fmt.Sprintf("Ваш текущий баланс: %d🌟", balance)
		}
		bot.Send(tgbotapi.NewMessage(fromID, msg))
		answerCallback(bot, callback.ID, "")
		return

	case data == "menu_market" && accessLevel == "worker":
		features.ShowShop(bot, db, callback.Message.Chat.ID, fromID)
		answerCallback(bot, callback.ID, "")
		return

	case strings.HasPrefix(data, "buy_product:") && accessLevel == "worker":
		handleBuyCallback(bot, db, callback)
		answerCallback(bot, callback.ID, "Покупка оформлена!")
		return

	case data == "history_orders" && accessLevel == "worker":
		list, err := database.SendHistoryOrders(db, fromID)
		msg := fmt.Sprintf("История заказов:\n%s", list)
		if err != nil {
			msg = "Ошибка загрузки истории"
		}
		bot.Send(tgbotapi.NewMessage(fromID, msg))
		answerCallback(bot, callback.ID, "")
		return

	default:
		bot.Send(tgbotapi.NewMessage(fromID, fmt.Sprintf("⛔ Ошибка доступа. Ваш уровень: %s", accessLevel)))
		answerCallback(bot, callback.ID, "")
		return
	}

}

func answerCallback(bot *tgbotapi.BotAPI, callbackID, text string) {
	cb := tgbotapi.NewCallback(callbackID, text)
	if _, err := bot.Request(cb); err != nil {
		log.Println("Ошибка отправки callback:", err)
	}
}
