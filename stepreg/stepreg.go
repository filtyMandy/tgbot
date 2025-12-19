package stepreg

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	_ "github.com/lib/pq"
)

// RegistrationHandler обрабатывает команды и сообщения, связанные с регистрацией пользователя.
// Возвращает true, если сообщение было полностью обработано (включая ошибки),
// и false, если сообщение требует дальнейшей обработки другими обработчиками.
func RegistrationHandler(bot *tgbotapi.BotAPI, db *sql.DB, update tgbotapi.Update) bool {
	if update.Message == nil || update.Message.From == nil {
		return false // Не обрабатываем, если нет сообщения или отправителя
	}
	user := update.Message.From
	userID := int64(user.ID) // Telegram ID - это int64

	var regState string
	var registrationStartTime sql.NullTime // Для nullable timestamp в PostgreSQL
	var userNameFromDB string              // Переменная для хранения имени пользователя из БД, если оно есть
	var adminTelegramID int64              // Переменная для ID админа ресторана
	var adminFound bool                    // Флаг, был ли найден админ

	// --- Получение состояния пользователя ---
	query := `SELECT reg_state, registration_start_time, name FROM users WHERE telegram_id = $1`
	err := db.QueryRow(query, userID).Scan(&regState, &registrationStartTime, &userNameFromDB)

	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("Пользователь с telegram_id %d не найден в таблице users. Инициируется регистрация.", userID)
		} else {
			log.Printf("Ошибка при чтении состояния пользователя для user_id %d: %v", userID, err)
			bot.Send(tgbotapi.NewMessage(userID, "Произошла ошибка при проверке вашего статуса. Попробуйте позже."))
			return true // Обработали, но с ошибкой
		}
	}

	// --- Обработка команды /start ---
	if update.Message.IsCommand() && update.Message.Command() == "start" {
		tx, err := db.Begin() // Объявляем tx здесь
		if err != nil {
			log.Printf("Ошибка начала транзакции для /start (user_id %d): %v", userID, err)
			bot.Send(tgbotapi.NewMessage(userID, "Произошла внутренняя ошибка. Попробуйте позже."))
			return true
		}
		defer tx.Rollback() // Откат транзакции

		_, err = tx.Exec(`INSERT INTO users (telegram_id, username, verified, reg_state, registration_start_time, name, table_number, rest_number)
                         VALUES ($1, $2, 0, 'waiting_registration_data', NOW(), NULL, NULL, NULL)
                         ON CONFLICT (telegram_id) DO UPDATE SET
                           reg_state = 'waiting_registration_data',
                           registration_start_time = NOW(),
                           name = NULL,
                           table_number = NULL,
                           rest_number = NULL`,
			userID, user.UserName)
		if err != nil {
			log.Printf("Ошибка INSERT/UPDATE ON CONFLICT для /start (user_id %d): %v", userID, err)
			bot.Send(tgbotapi.NewMessage(userID, "Произошла ошибка при регистрации. Попробуйте позже."))
			return true
		}

		if err = tx.Commit(); err != nil {
			log.Printf("Ошибка коммита транзакции для /start (user_id %d): %v", userID, err)
			bot.Send(tgbotapi.NewMessage(userID, "Произошла внутренняя ошибка. Попробуйте позже."))
			return true
		}

		msg := tgbotapi.NewMessage(userID, `👋 Здравствуйте!
Для регистрации введите данные в таком формате:
**[номер в расписании] [Ваше имя] [номер предприятия]**

Пример:
15 Петр 11047

*Имя может состоять из нескольких слов.*

*Для сброса регистрации введите /start заново.*`)
		msg.ParseMode = "Markdown"
		bot.Send(msg)
		return true // Обработано командой /start
	}

	// --- Обработка сообщений в состоянии ожидания данных регистрации ---
	if regState == "waiting_registration_data" {
		// Здесь tx не объявлен, поэтому ошибка "Unresolved reference 'tx'"
		// Нужно либо перенести объявление tx наружу, либо создавать его заново, когда он нужен.

		// Проверяем таймаут регистрации
		if registrationStartTime.Valid {
			currentTimeUTC := time.Now().UTC()
			if currentTimeUTC.Sub(registrationStartTime.Time) > 5*time.Minute {
				// !!! Важно: используем отдельную транзакцию для сброса, если она не была начата ранее
				txReset, err := db.Begin()
				if err != nil {
					log.Printf("Ошибка начала транзакции для сброса reg_state после таймаута (user_id %d): %v", userID, err)
					bot.Send(tgbotapi.NewMessage(userID, "Произошла внутренняя ошибка. Попробуйте позже."))
					return true
				}
				defer txReset.Rollback() // Откат, если что-то пойдет не так

				_, err = txReset.Exec(`UPDATE users SET reg_state=NULL, name=NULL, table_number=NULL, rest_number=NULL, registration_start_time=NULL WHERE telegram_id=$1`, userID)
				if err != nil {
					log.Printf("Ошибка сброса reg_state после таймаута для user_id %d: %v", userID, err)
				}
				txReset.Commit() // Коммитим только сброс

				msg := tgbotapi.NewMessage(userID, "👋 Время ожидания ввода данных истекло. Пожалуйста, введите /start для начала регистрации заново.")
				msg.ParseMode = "Markdown"
				bot.Send(msg)
				return true // Завершаем обработку
			}
		} else {
			// Неожиданная ситуация: reg_state = 'waiting_registration_data', но registration_start_time NULL.
			log.Printf("Предупреждение: reg_state 'waiting_registration_data' но registration_start_time NULL для user_id %d. Сбрасываем состояние.", userID)
			// !!! Используем новую транзакцию для сброса
			txReset, err := db.Begin()
			if err != nil {
				log.Printf("Ошибка начала транзакции для сброса reg_state (некорректный start_time) (user_id %d): %v", userID, err)
				bot.Send(tgbotapi.NewMessage(userID, "Произошла внутренняя ошибка. Попробуйте позже."))
				return true
			}
			defer txReset.Rollback()
			_, err = txReset.Exec(`UPDATE users SET reg_state=NULL, name=NULL, table_number=NULL, rest_number=NULL, registration_start_time=NULL WHERE telegram_id=$1`, userID)
			if err != nil {
				log.Printf("Ошибка сброса reg_state после некорректного start_time для user_id %d: %v", userID, err)
			}
			txReset.Commit()
			msg := tgbotapi.NewMessage(userID, "Произошла внутренняя ошибка с вашим статусом. Пожалуйста, введите /start для начала регистрации заново.")
			msg.ParseMode = "Markdown"
			bot.Send(msg)
			return true
		}

		messageText := update.Message.Text
		trimmedMessage := strings.TrimSpace(messageText)
		parts := strings.Fields(trimmedMessage)

		if len(parts) < 3 {
			// Некорректный формат ввода. Сбрасываем состояние.
			// !!! Используем новую транзакцию для сброса
			txReset, err := db.Begin()
			if err != nil {
				log.Printf("Ошибка начала транзакции для сброса reg_state (некорректный формат) (user_id %d): %v", userID, err)
				bot.Send(tgbotapi.NewMessage(userID, "Произошла внутренняя ошибка. Попробуйте позже."))
				return true
			}
			defer txReset.Rollback()

			_, err = txReset.Exec(`UPDATE users SET reg_state=NULL, name=NULL, table_number=NULL, rest_number=NULL, registration_start_time=NULL WHERE telegram_id=$1`, userID)
			if err != nil {
				log.Printf("Ошибка сброса reg_state после некорректного ввода для user_id %d: %v", userID, err)
			}
			txReset.Commit()

			msg := tgbotapi.NewMessage(userID, "❗️ Некорректный формат ввода. Пожалуйста, убедитесь, что вы указали номер расписания, ваше имя и номер предприятия через пробелы.\n\nПример: 15 Петр 1023\n\n*Для сброса регистрации введите /start заново.*")
			msg.ParseMode = "Markdown"
			bot.Send(msg)
			return true
		}

		restNumberStr := parts[len(parts)-1]
		tableNumberStr := parts[0]
		nameParts := parts[1 : len(parts)-1]
		nameInput := strings.Join(nameParts, " ")

		// Валидация полей
		_, err = strconv.Atoi(tableNumberStr)
		if err != nil {
			// !!! Используем новую транзакцию для сброса
			txReset, err := db.Begin()
			if err != nil {
				log.Printf("Ошибка начала транзакции для сброса reg_state (некорр. номер расписания) (user_id %d): %v", userID, err)
				bot.Send(tgbotapi.NewMessage(userID, "Произошла внутренняя ошибка. Попробуйте позже."))
				return true
			}
			defer txReset.Rollback()
			_, errExec := txReset.Exec(`UPDATE users SET reg_state=NULL, name=NULL, table_number=NULL, rest_number=NULL, registration_start_time=NULL WHERE telegram_id=$1`, userID)
			if errExec != nil {
				log.Printf("Ошибка сброса reg_state после некорректного ввода номера расписания для user_id %d: %v", userID, errExec)
			}
			txReset.Commit()
			msg := tgbotapi.NewMessage(userID, "❗️ Номер в расписании должен состоять только из цифр. Пожалуйста, введите /start для начала регистрации заново.")
			msg.ParseMode = "Markdown"
			bot.Send(msg)
			return true
		}

		_, err = strconv.Atoi(restNumberStr)
		if err != nil {
			// !!! Используем новую транзакцию для сброса
			txReset, err := db.Begin()
			if err != nil {
				log.Printf("Ошибка начала транзакции для сброса reg_state (некорр. номер предприятия) (user_id %d): %v", userID, err)
				bot.Send(tgbotapi.NewMessage(userID, "Произошла внутренняя ошибка. Попробуйте позже."))
				return true
			}
			defer txReset.Rollback()
			_, errExec := txReset.Exec(`UPDATE users SET reg_state=NULL, name=NULL, table_number=NULL, rest_number=NULL, registration_start_time=NULL WHERE telegram_id=$1`, userID)
			if errExec != nil {
				log.Printf("Ошибка сброса reg_state после некорректного ввода номера предприятия для user_id %d: %v", userID, errExec)
			}
			txReset.Commit()
			msg := tgbotapi.NewMessage(userID, "❗️ Номер предприятия должен состоять только из цифр. Пожалуйста, введите /start для начала регистрации заново.")
			msg.ParseMode = "Markdown"
			bot.Send(msg)
			return true
		}

		if nameInput == "" {
			// !!! Используем новую транзакцию для сброса
			txReset, err := db.Begin()
			if err != nil {
				log.Printf("Ошибка начала транзакции для сброса reg_state (пустое имя) (user_id %d): %v", userID, err)
				bot.Send(tgbotapi.NewMessage(userID, "Произошла внутренняя ошибка. Попробуйте позже."))
				return true
			}
			defer txReset.Rollback()

			_, errExec := txReset.Exec(`UPDATE users SET reg_state=NULL, name=NULL, table_number=NULL, rest_number=NULL, registration_start_time=NULL WHERE telegram_id=$1`, userID)
			if errExec != nil {
				log.Printf("Ошибка сброса reg_state после некорректного ввода имени для user_id %d: %v", userID, errExec)
			}
			txReset.Commit()

			msg := tgbotapi.NewMessage(userID, "❗️ Имя не может быть пустым. Пожалуйста, введите /start для начала регистрации заново.")
			msg.ParseMode = "Markdown"
			bot.Send(msg)
			return true
		}

		// --- Начинаем транзакцию для финального обновления данных и поиска админа ---
		tx, err := db.Begin() // Объявляем tx здесь, так как он нужен только в этом блоке
		if err != nil {
			log.Printf("Ошибка начала транзакции для финального обновления данных регистрации (user_id %d): %v", userID, err)
			bot.Send(tgbotapi.NewMessage(userID, "Произошла внутренняя ошибка. Попробуйте позже."))
			return true
		}
		defer tx.Rollback() // Откат этой транзакции, если она не будет успешно закоммичена

		// --- Поиск администратора ресторана ---
		err = tx.QueryRow(`SELECT telegram_id FROM users WHERE rest_number = $1 AND access_level = 'admin' LIMIT 1`, restNumberStr).Scan(&adminTelegramID)
		if err == sql.ErrNoRows {
			bot.Send(tgbotapi.NewMessage(userID, "❗️ Ресторан с таким номером не найден или у него еще не назначен администратор. Пожалуйста, введите /start для начала регистрации заново."))
			// Сбрасываем состояние пользователя.
			_, errExec := tx.Exec(`UPDATE users SET reg_state=NULL, name=NULL, table_number=NULL, rest_number=NULL, registration_start_time=NULL WHERE telegram_id=$1`, userID)
			if errExec != nil {
				log.Printf("Ошибка сброса reg_state после ненахождения ресторана для user_id %d: %v", userID, errExec)
			}
			// Транзакция будет отменена при выходе из функции благодаря defer.
			return true
		}
		if err != nil {
			log.Printf("Ошибка поиска администратора ресторана (rest_number %s, user_id %d): %v", restNumberStr, userID, err)
			bot.Send(tgbotapi.NewMessage(userID, "Произошла ошибка при поиске ресторана. Попробуйте позже!"))
			return true
		}
		adminFound = true // Админ найден

		// --- Обновляем данные пользователя в базе данных ---
		_, err = tx.Exec(`UPDATE users SET name=$1, table_number=$2, rest_number=$3, reg_state=NULL, registration_start_time=NULL WHERE telegram_id=$4`,
			nameInput, tableNumberStr, restNumberStr, userID)
		if err != nil {
			log.Printf("Ошибка обновления данных пользователя при регистрации (user_id %d): %v", userID, err)
			bot.Send(tgbotapi.NewMessage(userID, "Произошла ошибка при сохранении ваших данных. Попробуйте позже!"))
			return true
		}

		// --- Коммитим транзакцию ---
		if err = tx.Commit(); err != nil {
			log.Printf("Ошибка коммита транзакции для регистрации (user_id %d): %v", userID, err)
			bot.Send(tgbotapi.NewMessage(userID, "Произошла внутренняя ошибка. Попробуйте позже."))
			return true
		}

		// --- Отправляем сообщение пользователю об успешной регистрации ---
		bot.Send(tgbotapi.NewMessage(userID, "✅ Спасибо! Ваши данные переданы на модерацию. Ожидайте подтверждения."))

		// --- Отправляем уведомление админу (если админ был найден) ---
		if adminFound {
			txt := fmt.Sprintf(
				"✨ Новая регистрация!\n\n👤 **Имя:** %s\n#️⃣ **Номер в расписании:** %s\n🏢 **Номер предприятия (ПБО):** %s\n\n🌐 **Username:** @%s\n🆔 **Telegram ID:** `%d`",
				nameInput, tableNumberStr, restNumberStr, user.UserName, userID)

			approveKeyboard := tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("✅ Работник", fmt.Sprintf("approve:worker:%d", userID)),
					tgbotapi.NewInlineKeyboardButtonData("👑 Менеджер", fmt.Sprintf("approve:manager:%d", userID)),
					tgbotapi.NewInlineKeyboardButtonData("❌ Отклонить", fmt.Sprintf("reject:%d", userID)),
				),
			)
			adminMsg := tgbotapi.NewMessage(adminTelegramID, txt)
			adminMsg.ReplyMarkup = approveKeyboard
			adminMsg.ParseMode = tgbotapi.ModeMarkdown
			if _, err := bot.Send(adminMsg); err != nil {
				log.Printf("Ошибка отправки сообщения админу (admin_id %d, user_id %d): %v", adminTelegramID, userID, err)
			}
		}
		return true // Сообщение полностью обработано
	}

	return false // Сообщение не было обработано этим хэндлером
}
