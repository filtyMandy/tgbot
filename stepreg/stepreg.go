package stepreg

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// RegistrationHandler обрабатывает команды и сообщения, связанные с регистрацией пользователя
// в формате "номер_расписания Имя номер_предприятия".
// Возвращает true, если сообщение было обработано, иначе false.
func RegistrationHandler(bot *tgbotapi.BotAPI, db *sql.DB, update tgbotapi.Update) bool {
	if update.Message == nil || update.Message.From == nil {
		return false
	}
	user := update.Message.From
	userID := user.ID

	var regState string
	var registrationStartTime sql.NullTime // <-- Используйте sql.NullTime для потенциально NULL значений

	// --- Получение состояния пользователя ---
	// Получаем текущее состояние регистрации и время начала последней регистрации.
	// Обратите внимание на порядок полей в SELECT и Scan
	err := db.QueryRow(`SELECT reg_state, registration_start_time FROM users WHERE telegram_id=?`,
		userID).Scan(&regState, &registrationStartTime) // <-- Сканируем в regState и registrationStartTime
	if err != nil && err != sql.ErrNoRows {
		log.Printf("Ошибка при чтении состояния пользователя для user_id %d: %v", userID, err)
		bot.Send(tgbotapi.NewMessage(userID, "Произошла ошибка при проверке вашего статуса. Попробуйте позже."))
		return true // Обработали, но с ошибкой
	}

	// --- Обработка команд ---

	// 1. Команда /start - инициирует регистрацию
	if update.Message.IsCommand() && update.Message.Command() == "start" {
		tx, err := db.Begin()
		if err != nil {
			log.Printf("Ошибка начала транзакции для /start (user_id %d): %v", userID, err)
			bot.Send(tgbotapi.NewMessage(userID, "Произошла внутренняя ошибка. Попробуйте позже."))
			return true
		}
		defer tx.Rollback() // Откат, если что-то пойдет не так

		// Создаем пользователя, если его нет, или обновляем/игнорируем, если существует.
		// Устанавливаем состояние ожидания данных регистрации и текущее время.
		// Используем datetime('now') для SQLite
		_, err = tx.Exec(`INSERT OR IGNORE INTO users (telegram_id, username, verified, reg_state, registration_start_time)
                         VALUES (?, ?, 0, 'waiting_registration_data', datetime('now'))`, userID, user.UserName)
		if err != nil {
			log.Printf("Ошибка INSERT OR IGNORE для /start (user_id %d): %v", userID, err)
			bot.Send(tgbotapi.NewMessage(userID, "Произошла ошибка при регистрации. Попробуйте позже."))
			return true
		}

		// Обновляем состояние и очищаем предыдущие данные, если пользователь уже был.
		// Также устанавливаем текущее время для нового старта регистрации.
		_, err = tx.Exec(`UPDATE users SET reg_state='waiting_registration_data', name='', table_number='', rest_number='', registration_start_time=datetime('now')
                         WHERE telegram_id=?`, userID)
		if err != nil {
			log.Printf("Ошибка UPDATE для /start (user_id %d): %v", userID, err)
			bot.Send(tgbotapi.NewMessage(userID, "Произошла ошибка при обновлении данных. Попробуйте позже."))
			return true
		}

		if err = tx.Commit(); err != nil {
			log.Printf("Ошибка коммита транзакции для /start (user_id %d): %v", userID, err)
			bot.Send(tgbotapi.NewMessage(userID, "Произошла внутренняя ошибка. Попробуйте позже."))
			return true
		}

		bot.Send(tgbotapi.NewMessage(userID, `👋 Здравствуйте!
Для регистрации введите данные в таком формате:
**[номер в расписании] [Ваше имя] [номер предприятия]**

Пример:
15 Петр 11047

*Имя может состоять из нескольких слов.*

*Для сброса регистрации введите /start заново.*`))
		return true
	}

	// --- Обработка сообщений в состоянии ожидания данных регистрации ---

	if regState == "waiting_registration_data" {
		// Проверяем таймаут регистрации (5 минут)
		// Используем registrationStartTime.Time, если registrationStartTime.Valid == true
		if registrationStartTime.Valid && time.Since(registrationStartTime.Time) > 5*time.Minute {
			// Сбрасываем состояние и просим начать заново
			// Устанавливаем registration_start_time в NULL, так как состояние сбрасывается
			_, err := db.Exec(`UPDATE users SET reg_state='', name='', table_number='', rest_number='', registration_start_time=NULL WHERE telegram_id=?`, userID)
			if err != nil {
				log.Printf("Ошибка сброса reg_state после таймаута для user_id %d: %v", userID, err)
			}
			bot.Send(tgbotapi.NewMessage(userID, "👋 Время ожидания ввода данных истекло. Пожалуйста, введите /start для начала регистрации заново."))
			return true // Завершаем обработку, пользователь получил уведомление
		}

		messageText := update.Message.Text
		trimmedMessage := strings.TrimSpace(messageText)
		parts := strings.Fields(trimmedMessage)

		// Ожидаем как минимум 3 части: номер расписания, имя (может быть несколько слов), номер предприятия.
		if len(parts) < 3 {
			bot.Send(tgbotapi.NewMessage(userID, "❗️ Некорректный формат ввода. Пожалуйста, убедитесь, что вы указали номер расписания, ваше имя и номер предприятия через пробелы.\n\nПример: 15 Петр 1023\n\n*Для сброса регистрации введите /start заново.*"))
			// Сбрасываем состояние пользователя, чтобы он мог начать заново.
			// Устанавливаем registration_start_time в NULL, так как состояние сбрасывается
			_, err := db.Exec(`UPDATE users SET reg_state='', name='', table_number='', rest_number='', registration_start_time=NULL WHERE telegram_id=?`, userID)
			if err != nil {
				log.Printf("Ошибка сброса reg_state после некорректного ввода для user_id %d: %v", userID, err)
			}
			return true
		}

		restNumberStr := parts[len(parts)-1]
		tableNumberStr := parts[0]
		nameParts := parts[1 : len(parts)-1]
		nameInput := strings.Join(nameParts, " ")

		// --- Валидация отдельных полей ---

		// 1. Валидация номера расписания
		_, err = strconv.Atoi(tableNumberStr)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(userID, "❗️ Номер в расписании должен состоять только из цифр. Пожалуйста, введите /start для начала регистрации заново."))
			// Сбрасываем состояние пользователя, так как ввод некорректен.
			// Устанавливаем registration_start_time в NULL, так как состояние сбрасывается
			_, errExec := db.Exec(`UPDATE users SET reg_state='', name='', table_number='', rest_number='', registration_start_time=NULL WHERE telegram_id=?`, userID)
			if errExec != nil {
				log.Printf("Ошибка сброса reg_state после некорректного ввода номера расписания для user_id %d: %v", userID, errExec)
			}
			return true
		}

		// 2. Валидация номера предприятия (должен быть числом)
		_, err = strconv.Atoi(restNumberStr)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(userID, "❗️ Номер предприятия должен состоять только из цифр. Пожалуйста, введите /start для начала регистрации заново."))
			// Сбрасываем состояние пользователя, так как ввод некорректен.
			// Устанавливаем registration_start_time в NULL, так как состояние сбрасывается
			_, errExec := db.Exec(`UPDATE users SET reg_state='', name='', table_number='', rest_number='', registration_start_time=NULL WHERE telegram_id=?`, userID)
			if errExec != nil {
				log.Printf("Ошибка сброса reg_state после некорректного ввода номера предприятия для user_id %d: %v", userID, errExec)
			}
			return true
		}

		// 3. Проверка имени (не должно быть пустым после удаления пробелов)
		if nameInput == "" {
			bot.Send(tgbotapi.NewMessage(userID, "❗️ Имя не может быть пустым. Пожалуйста, введите /start для начала регистрации заново."))
			// Сбрасываем состояние пользователя, так как ввод некорректен.
			// Устанавливаем registration_start_time в NULL, так как состояние сбрасывается
			_, errExec := db.Exec(`UPDATE users SET reg_state='', name='', table_number='', rest_number='', registration_start_time=NULL WHERE telegram_id=?`, userID)
			if errExec != nil {
				log.Printf("Ошибка сброса reg_state после некорректного ввода имени для user_id %d: %v", userID, errExec)
			}
			return true
		}

		// --- Поиск админа ресторана ---
		var adminTelegramID int64
		adminFound := false

		// Начинаем новую транзакцию для выполнения операций с БД
		tx, err := db.Begin()
		if err != nil {
			log.Printf("Ошибка начала транзакции для обновления данных регистрации (user_id %d): %v", userID, err)
			bot.Send(tgbotapi.NewMessage(userID, "Произошла внутренняя ошибка. Попробуйте позже."))
			return true
		}
		defer tx.Rollback() // Откат, если что-то пойдет не так

		// Ищем администратора для указанного номера предприятия
		err = tx.QueryRow(`SELECT telegram_id FROM users WHERE rest_number=? AND access_level='admin' LIMIT 1`, restNumberStr).Scan(&adminTelegramID)
		if err == sql.ErrNoRows {
			// Ресторан не найден или у него нет админа.
			bot.Send(tgbotapi.NewMessage(userID, "❗️ Ресторан с таким номером не найден или у него еще не назначен администратор. Пожалуйста, введите /start для начала регистрации заново."))
			// Сбрасываем состояние пользователя.
			// Устанавливаем registration_start_time в NULL, так как состояние сбрасывается
			_, errExec := tx.Exec(`UPDATE users SET reg_state='', name='', table_number='', rest_number='', registration_start_time=NULL WHERE telegram_id=?`, userID)
			if errExec != nil {
				log.Printf("Ошибка сброса reg_state после ненахождения ресторана для user_id %d: %v", userID, errExec)
			}
			// Не коммитим, так как это фактически откат всех изменений, если бы они были.
			return true
		}
		if err != nil {
			// Другая ошибка при поиске админа.
			log.Printf("Ошибка поиска администратора ресторана (rest_number %s, user_id %d): %v", restNumberStr, userID, err)
			bot.Send(tgbotapi.NewMessage(userID, "Произошла ошибка при поиске ресторана. Попробуйте позже!"))
			return true
		}
		adminFound = true // Админ найден

		// --- Обновляем данные пользователя ---
		// Устанавливаем все данные и сбрасываем состояние регистрации.
		// Устанавливаем registration_start_time в NULL, так как регистрация завершена (переходит в другое состояние или верифицируется).
		_, err = tx.Exec(`UPDATE users SET name=?, table_number=?, rest_number=?, reg_state='', registration_start_time=NULL WHERE telegram_id=?`,
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

		// --- Отправляем сообщение пользователю ---
		bot.Send(tgbotapi.NewMessage(userID, "✅ Спасибо! Ваши данные переданы на модерацию. Ожидайте подтверждения."))

		// --- Отправляем уведомление админу ---
		if adminFound { // Отправляем только если админ был найден
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
			adminMsg.ParseMode = tgbotapi.ModeMarkdown // Используем Markdown для форматирования
			if _, err := bot.Send(adminMsg); err != nil {
				log.Printf("Ошибка отправки сообщения админу (admin_id %d, user_id %d): %v", adminTelegramID, userID, err)
				// Не возвращаем ошибку, так как регистрация пользователя прошла успешно
			}
		}
		return true
	}

	// Если сообщение не было обработано (например, пользователь отправил что-то вне контекста регистрации),
	// возвращаем false, чтобы его мог обработать другой хэндлер.
	return false
}
