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
	chatID := callback.Message.Chat.ID      // ID чата, где находится сообщение с кнопками
	messageID := callback.Message.MessageID // ID сообщения, которое нужно отредактировать

	/*adminTelegramID, err := database.GetAdminID(db, fromID)
	if err != nil {
		log.Println(err)
	}*/

	accessLevel, err := database.GetAccessLevel(db, fromID)
	if err != nil {
		log.Println(err)
	}
	log.Printf("Callback data: %s, user: %d, level: %s", data, fromID, accessLevel)

	switch {
	case strings.HasPrefix(data, "registrations:button") && accessLevel == "admin":
		dep, err := database.GetUserDep(db, fromID)
		if err != nil {
			log.Printf("Ошибка поиска предприятия для admin %d: %v", fromID, err)
			bot.Send(tgbotapi.NewMessage(fromID, "Ошибка поиска вашего предприятия."))
			return
		}

		err = database.SendUserListWithPagination(
			bot, db, fromID, dep, 0, 0, // page 0, verifiedStatus 0 (неподтвержденные)
			"registrations:list",       // Префикс для кнопок выбора пользователя
			"registrations:pagination", // Префикс для кнопок пагинации
			"Выберите пользователя для подтверждения", // Заголовок сообщения
		)
		if err != nil {
			log.Printf("Ошибка при отправке списка ожидающих регистрации: %v", err)
			bot.Send(tgbotapi.NewMessage(fromID, "Не удалось получить список ожидающих регистрации."))
		}
		answerCallback(bot, callback.ID, "")

	case strings.HasPrefix(data, "registrations:pagination:") && accessLevel == "admin":
		parts := strings.Split(data, ":")
		// Ожидаем 5 частей: registrations:pagination:page:dep:verifiedStatus
		if len(parts) != 5 {
			log.Printf("Некорректный формат callback data для пагинации регистраций: %s", data)
			answerCallback(bot, callback.ID, "Некорректные данные пагинации.")
			return
		}
		page, err := strconv.Atoi(parts[2])
		if err != nil {
			log.Printf("Ошибка парсинга страницы для пагинации регистраций: %v", err)
			answerCallback(bot, callback.ID, "Ошибка страницы.")
			return
		}
		dep := parts[3]
		verifiedStatus, err := strconv.Atoi(parts[4])
		if err != nil {
			log.Printf("Ошибка парсинга статуса верификации для пагинации регистраций: %v", err)
			answerCallback(bot, callback.ID, "Ошибка статуса.")
			return
		}
		// Используем жестко заданные префиксы, так как пагинация "регистраций"
		userSelectPrefix := "registrations:list"
		paginationPrefix := "registrations:pagination"
		heading := "Выберите пользователя для подтверждения"

		err = database.SendUserListWithPagination(
			bot, db, fromID, dep, page, verifiedStatus,
			userSelectPrefix, paginationPrefix, heading,
		)
		if err != nil {
			log.Printf("Ошибка при отправке страницы регистраций (page %d, dep %s, verified %d): %v", page, dep, verifiedStatus, err)
			bot.Send(tgbotapi.NewMessage(fromID, "Не удалось обновить список регистраций."))
		}
		answerCallback(bot, callback.ID, "")
		return

	case strings.HasPrefix(data, "registrations:list:") && accessLevel == "admin":
		parts := strings.Split(data, ":")
		if len(parts) != 3 {
			log.Printf("Некорректный формат callback data для выбора пользователя: %s", data)
			answerCallback(bot, callback.ID, "Ошибка данных пользователя.")
			return
		}
		userID, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			log.Printf("Ошибка парсинга userID из callback data: %v", err)
			answerCallback(bot, callback.ID, "Ошибка userID.")
			return
		}

		// Получаем информацию о пользователе из БД
		// func GetRegInfo(db *sql.DB, userID int64) (verified int, restNumber string, name string, tableNum string, username string, err error)
		verify, rest, name, tableNum, username, err := database.GetRegInfo(db, userID)
		if err != nil {
			log.Printf("Ошибка при получении информации о пользователе %d: %v", userID, err)
			bot.Send(tgbotapi.NewMessage(chatID, "Не удалось получить информацию о пользователе.")) // Отправляем сообщение в текущий чат
			answerCallback(bot, callback.ID, "Ошибка.")
			return
		}

		if verify == 1 {
			// Если пользователь уже подтвержден, информируем администратора и обновляем сообщение
			editMsg := tgbotapi.NewEditMessageText(
				chatID,
				messageID,
				"Пользователь уже подтвержден.",
			)
			_, sendErr := bot.Send(editMsg)
			if sendErr != nil {
				log.Printf("Ошибка обновления сообщения о подтвержденном пользователе: %v", sendErr)
			}
			answerCallback(bot, callback.ID, "Уже подтвержден.")
			return
		}

		// Формируем текст сообщения
		txt := fmt.Sprintf(
			"✨ Подтвердите регистрацию!\n\n👤 **Имя:** %s\n#️⃣ **Номер в расписании:** %s"+
				"\n🏢 **Номер предприятия (ПБО):** %s\n\n🌐 **Username:** @%s\n🆔 **Telegram ID:** `%d`",
			name, tableNum, rest, username, userID)

		// Инлайн-клавиатура с выбором допуска
		approveKeyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✅ Работник", fmt.Sprintf("approve:worker:%d", userID)),
				tgbotapi.NewInlineKeyboardButtonData("👑 Менеджер", fmt.Sprintf("approve:manager:%d", userID)),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("❌ Отклонить", fmt.Sprintf("reject:%d", userID)),
			),
		)

		editMsg := tgbotapi.NewEditMessageTextAndMarkup(
			chatID,    // ID текущего чата
			messageID, // ID сообщения, на которое была нажата кнопка
			txt,
			approveKeyboard,
		)
		editMsg.ParseMode = tgbotapi.ModeMarkdown

		if _, err := bot.Send(editMsg); err != nil {
			log.Printf("Ошибка редактирования сообщения для admin %d (user_id %d): %v", fromID, userID, err)
			bot.Send(tgbotapi.NewMessage(chatID, "Не удалось отобразить опции подтверждения.")) // Отправляем новое сообщение, если редактирование не удалось
		}
		answerCallback(bot, callback.ID, "")
		return

	case strings.HasPrefix(data, "super_user") && fromID == superUser:
		handleSuper(bot, db, callback, userState)
		answerCallback(bot, callback.ID, "")

	case strings.HasPrefix(data, "approve:") && accessLevel == "admin":
		parts := strings.Split(data, ":")
		if len(parts) == 3 {
			role := parts[1]
			uid, err := strconv.ParseInt(parts[2], 10, 64) // Добавил обработку ошибки
			if err != nil {
				log.Printf("Ошибка парсинга UID в approve: %v", err)
				answerCallback(bot, callback.ID, "Ошибка UID.")
				return
			}
			var verify int
			verify, _, _, _, _, err = database.GetRegInfo(db, uid)
			if err != nil {
				log.Printf("GetRegInfoEroor: %v", err)
				answerCallback(bot, callback.ID, "Ошибка поиска UID.")
				return
			}
			if verify == 1 {
				answerCallback(bot, callback.ID, "Регистрация уже обработана")
				return
			}

			// Обновление пользователя в БД
			_, err = db.Exec(`UPDATE users SET access_level=$1, verified=1, current_balance=0, last_ts=0 WHERE telegram_id=$2`, role, uid)
			if err != nil {
				log.Printf("Ошибка обновления пользователя %d в БД (approve): %v", uid, err)
				bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при подтверждении пользователя."))
				answerCallback(bot, callback.ID, "Ошибка БД.")
				return
			}

			// Отправка подтверждения пользователю
			userMsg := tgbotapi.NewMessage(uid, fmt.Sprintf("✅ Регистрация подтверждена! Ваш статус: %s.\n/menu — доступ к функциям.", role))
			userMsg.ParseMode = tgbotapi.ModeMarkdown // Если хотите использовать Markdown в userMsg
			_, err = bot.Send(userMsg)
			if err != nil {
				log.Printf("Ошибка отправки сообщения пользователю %d о подтверждении: %v", uid, err)
			}

			// 🚀 ИСПРАВЛЕНИЕ ЗДЕСЬ: Редактируем сообщение для администратора
			confirmationTextForAdmin := fmt.Sprintf("✅ Пользователь `%d` подтвержден как **%s**.", uid, role)
			editMsg := tgbotapi.NewEditMessageText(
				chatID,    // Чат администратора
				messageID, // Сообщение, которое было нажато
				confirmationTextForAdmin,
			)
			editMsg.ParseMode = tgbotapi.ModeMarkdown // Если хотите использовать Markdown
			_, err = bot.Request(editMsg)             // Используем bot.Request() для редактирования
			if err != nil {
				log.Printf("Ошибка редактирования сообщения для админа %d после подтверждения: %v", fromID, err)
				// В случае ошибки редактирования, можно отправить новое сообщение админу
				bot.Send(tgbotapi.NewMessage(chatID, confirmationTextForAdmin))
			}

			answerCallback(bot, callback.ID, "Пользователь принят.")
			return
		}

	// --- 2. Обработка кнопки "❌ Отклонить" ---
	case strings.HasPrefix(data, "reject:") && accessLevel == "admin":
		parts := strings.Split(data, ":")
		if len(parts) == 2 {
			uid, err := strconv.ParseInt(parts[1], 10, 64) // Добавил обработку ошибки
			if err != nil {
				log.Printf("Ошибка парсинга UID в reject: %v", err)
				answerCallback(bot, callback.ID, "Ошибка UID.")
				return
			}

			var verify int
			verify, _, _, _, _, err = database.GetRegInfo(db, uid)
			if err != nil {
				log.Printf("GetRegInfoEroor: %v", err)
				answerCallback(bot, callback.ID, "Ошибка поиска UID.")
				return
			}
			if verify == 1 {
				answerCallback(bot, callback.ID, "Регистрация уже обработана")
				return
			}

			// Обновление пользователя в БД (помечаем как отклоненного)
			err = database.DeleteUser(db, uid)
			if err != nil {
				log.Printf("Ошибка обновления пользователя %d в БД (reject): %v", uid, err)
				bot.Send(tgbotapi.NewMessage(chatID, "❌ Ошибка при отклонении пользователя."))
				answerCallback(bot, callback.ID, "Ошибка БД.")
				return
			}

			// Отправка сообщения пользователю об отклонении
			userMsg := tgbotapi.NewMessage(uid, "❌ Ваша регистрация отклонена администратором.")
			_, err = bot.Send(userMsg)
			if err != nil {
				log.Printf("Ошибка отправки сообщения пользователю %d об отклонении: %v", uid, err)
			}

			confirmationTextForAdmin := fmt.Sprintf("❌ Пользователь `%d` отклонен.", uid)
			editMsg := tgbotapi.NewEditMessageText(
				chatID,    // Чат администратора
				messageID, // Сообщение, которое было нажато
				confirmationTextForAdmin,
			)
			editMsg.ParseMode = tgbotapi.ModeMarkdown // Если хотите использовать Markdown
			_, err = bot.Request(editMsg)             // Используем bot.Request() для редактирования
			if err != nil {
				log.Printf("Ошибка редактирования сообщения для админа %d после отклонения: %v", fromID, err)
				// В случае ошибки редактирования, можно отправить новое сообщение админу
				bot.Send(tgbotapi.NewMessage(chatID, confirmationTextForAdmin))
			}

			answerCallback(bot, callback.ID, "Заявка отклонена.") // Точка в конце для единообразия
			return
		}

	case data == "menu_admin_setbal" && accessLevel == "admin":
		dep, err := database.GetUserDep(db, fromID)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(fromID, "Ошибка поиска вашего предприятия."))
			return
		}
		database.SendWorkersList(bot, db, fromID, "correction", dep, 0, 1, "worker")
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
		handleShopEdit(bot, db, callback, shopState, accessLevel)
		answerCallback(bot, callback.ID, "")
		return

	case strings.HasPrefix(data, "orders") && accessLevel == "admin":
		handleOrderComplete(bot, db, callback, accessLevel)
		answerCallback(bot, callback.ID, "")
		return

	case strings.HasPrefix(data, "topup_") && (accessLevel == "admin" || accessLevel == "manager"):
		handleTopUpCallback(bot, db, fromID, data, callback, "worker")
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
