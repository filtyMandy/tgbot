package callback

import (
	"database/sql"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log"
	"strconv"
	"strings"
	"tbViT/database" // Убедитесь, что пути импорта корректны
)

// handleTopUpCallback обрабатывает callback-запросы, связанные с пополнением баланса.
// Он парсит callback-данные, чтобы определить действие (выбор работника, выбор суммы)
// и выполняет соответствующую операцию, напрямую используя ID из callback-данных.
func handleTopUpCallback(
	bot *tgbotapi.BotAPI,
	db *sql.DB,
	fromID int64, // ID пользователя, который инициировал callback (менеджер/админ)
	data string, // callback_data
	callback *tgbotapi.CallbackQuery, // объект callback-query
	accessLevel string,
) {
	switch {
	// --- Инициация процесса пополнения ---
	case data == "topup_":
		dep, err := database.GetUserDep(db, fromID)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(fromID, "Ошибка поиска вашего предприятия."))
			return
		}
		// Отправляем список работников. status "topup_select_worker" - это префикс для callback'ов выбора работника.
		err = database.SendWorkersList(bot, db, fromID, "topup_select_worker", dep, 0, 1, "worker")
		if err != nil {
			bot.Send(tgbotapi.NewMessage(fromID, "Не удалось отобразить список работников."))
		}
		answerCallback(bot, callback.ID, "")
		return

	// --- Обработка выбора суммы пополнения ---
	case strings.HasPrefix(data, "topup_amount:"):
		// Ожидаемый формат: "topup_amount:сумма:workerID"
		parts := strings.Split(data, ":")
		if len(parts) != 3 {
			bot.Send(tgbotapi.NewMessage(fromID, "Ошибка: некорректный формат данных суммы пополнения."))
			answerCallback(bot, callback.ID, "")
			return
		}

		amountStr := parts[1]
		workerIDStr := parts[2]

		amount, err := strconv.Atoi(amountStr)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(fromID, "Ошибка: неверная сумма пополнения."))
			answerCallback(bot, callback.ID, "")
			return
		}

		workerID, err := strconv.ParseInt(workerIDStr, 10, 64)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(fromID, "Ошибка: неверный ID работника."))
			answerCallback(bot, callback.ID, "")
			return
		}

		// Проверяем, что workerID валиден
		if workerID <= 0 {
			bot.Send(tgbotapi.NewMessage(fromID, "Ошибка: работник не выбран или ID некорректен."))
			answerCallback(bot, callback.ID, "")
			return
		}

		// Выполняем пополнение баланса работника
		msg, isSuccess, err := database.TopUpBalance(db, workerID, amount)
		if err != nil {
			log.Printf("Ошибка TopUpBalance для workerID %d: %v", workerID, err)
			bot.Send(tgbotapi.NewMessage(fromID, "Произошла ошибка при пополнении баланса."))
			answerCallback(bot, callback.ID, "")
			return
		}

		bot.Send(tgbotapi.NewMessage(fromID, msg))

		if isSuccess {
			bot.Send(tgbotapi.NewMessage(workerID, fmt.Sprintf("Ваш баланс пополнен на %d 🌟!", amount)))

			err = database.LogBalanceTransaction(db, fromID, workerID, amount)
			if err != nil {
				log.Printf("Ошибка записи в историю баланса для workerID %d: %v", workerID, err)
			}
		}

		// Сбрасываем tmp_field менеджера, т.к. операция завершена
		// Это сделано для очистки состояния, даже если оно больше не используется напрямую.
		_, err = db.Exec(`UPDATE users SET tmp_field='' WHERE telegram_id=?`, fromID)
		if err != nil {
			log.Printf("Ошибка сброса tmp_field для менеджера %d: %v", fromID, err)
		}
		answerCallback(bot, callback.ID, "Готово!")
		return

	//  Обработка выбора работника (предполагается, что доступ уже проверен)
	case strings.HasPrefix(data, "topup_select_worker:"):
		parts := strings.Split(data, ":")

		if len(parts) >= 2 && parts[0] == "topup_select_worker" {
			if len(parts) == 2 {
				// Выбор конкретного работника
				workerID, err := strconv.ParseInt(parts[1], 10, 64)
				if err != nil {
					bot.Send(tgbotapi.NewMessage(fromID, "Ошибка: некорректный ID работника."))
					answerCallback(bot, callback.ID, "")
					return
				}

				workerInfo := database.GetWorkerInfo(db, workerID)
				if err != nil {
					log.Printf("Ошибка получения информации о работнике (ID %d): %v", workerID, err)
					bot.Send(tgbotapi.NewMessage(fromID, "Ошибка получения информации о работнике."))
					answerCallback(bot, callback.ID, "")
					return
				}

				// Формируем кнопки для выбора суммы, включая workerID в callback-данные
				replyMarkup := tgbotapi.NewInlineKeyboardMarkup(
					tgbotapi.NewInlineKeyboardRow(
						tgbotapi.NewInlineKeyboardButtonData("1🌟", fmt.Sprintf("topup_amount:1:%d", workerID)),
						tgbotapi.NewInlineKeyboardButtonData("2🌟", fmt.Sprintf("topup_amount:2:%d", workerID)),
					),
				)

				msg := tgbotapi.NewMessage(fromID, fmt.Sprintf("%s\n\nВыберите сумму пополнения:", workerInfo))
				msg.ReplyMarkup = replyMarkup

				bot.Send(msg)
				answerCallback(bot, callback.ID, "")
				return

			} else if len(parts) == 4 {
				// Обработка пагинации (перелистывание страниц)
				page, err := strconv.Atoi(parts[1])
				if err != nil {
					bot.Send(tgbotapi.NewMessage(fromID, "Ошибка: некорректный номер страницы."))
					answerCallback(bot, callback.ID, "")
					return
				}
				status := parts[2]
				dep := parts[3]

				err = database.SendWorkersList(bot, db, fromID, status, dep, page, 1, "worker")
				if err != nil {
					log.Printf("Ошибка при перелистывании списка работников (page %d, dep %s): %v", page, dep, err)
					bot.Send(tgbotapi.NewMessage(fromID, "Ошибка при перелистывании списка."))
				}
				answerCallback(bot, callback.ID, "")
				return
			}
		}
		bot.Send(tgbotapi.NewMessage(fromID, "❌ Ошибка при выборе работника."))
		answerCallback(bot, callback.ID, "")
		return
	}

	// Если data не подходит ни под одно из условий выше
	answerCallback(bot, callback.ID, "")
}
