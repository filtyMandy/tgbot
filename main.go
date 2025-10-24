package main

import (
	"database/sql"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // Регистрируем драйвер для database/sql
	"github.com/joho/godotenv"
	"log"
	"os"
	"strconv"
	"tbViT/callback"
	"tbViT/database"
	"tbViT/features"
	"tbViT/stepreg"
)

var (
	userCommands = []tgbotapi.BotCommand{
		{Command: "menu", Description: "Меню"},
	}
)

// userState и shopState остаются без изменений, так как это структуры Go, а не специфичные для БД типы.
var userState = make(map[int64]*callback.CorrectionState)
var shopState = make(map[int64]*callback.CorrectionState)

func main() {

	err := godotenv.Load()
	if err != nil {
		log.Println("Warning: Could not load .env file. Ensure environment variables are set.")
	}

	botToken := os.Getenv("TOCKEN")
	if botToken == "" {
		panic("Missing token: TOCKEN environment variable is not set.")
	}
	superUserStr := os.Getenv("TELEGRAM_SUPER_USER")
	if superUserStr == "" {
		panic("Missing super user ID: TELEGRAM_SUPER_USER environment variable is not set.")
	}
	superUser, err := strconv.ParseInt(superUserStr, 10, 64)
	if err != nil {
		panic(fmt.Sprintf("Invalid TELEGRAM_SUPER_USER format: %v", err))
	}

	// Строка подключения к PostgreSQL
	postgresDSN := os.Getenv("POSTGRES_DSN")
	if postgresDSN == "" {
		panic("POSTGRES_DSN environment variable is not set.")
	}

	// --- Открытие соединения с базой данных PostgreSQL ---
	db, err := sql.Open("pgx", postgresDSN) // Используем "pgx" как драйвер
	if err != nil {
		log.Fatalf("Failed to open PostgreSQL database connection: %v", err)
	}
	// Важно: defer db.Close() должно идти после успешного открытия
	defer db.Close()

	// Проверка соединения с БД
	err = db.Ping()
	if err != nil {
		log.Fatalf("Failed to ping PostgreSQL database: %v", err)
	}
	log.Println("Successfully connected to PostgreSQL database.")

	// --- Создание таблиц (если не существуют) ---
	// PostgreSQL синтаксис для CREATE TABLE:
	// INTEGER PRIMARY KEY AUTOINCREMENT -> SERIAL PRIMARY KEY (или BIGSERIAL PRIMARY KEY для больших ID)
	// TEXT -> TEXT
	// INTEGER -> INTEGER
	// DATETIME -> TIMESTAMP WITHOUT TIME ZONE (или TIMESTAMP WITH TIME ZONE, если нужно учитывать часовые пояса)
	// TIMESTAMP DEFAULT CURRENT_TIMESTAMP -> TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP (рекомендуется)
	// BIGINT для telegram_id, так как он может быть большим.
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS users (
		id SERIAL PRIMARY KEY, -- SERIAL автоматически создает последовательность и BIGINT
		telegram_id BIGINT UNIQUE NOT NULL,
		username TEXT,
		name TEXT,
		table_number TEXT,
		rest_number INTEGER,
		access_level TEXT,
		verified INTEGER,
		reg_state TEXT,
        current_balance INTEGER DEFAULT 0,
        all_time_balance INTEGER DEFAULT 0,
        last_ts BIGINT DEFAULT 0, -- Unix timestamp
        tmp_field TEXT,
        special_roll TEXT,
        registration_start_time TIMESTAMP WITH TIME ZONE -- TIMESTAMP WITH TIME ZONE предпочтительнее
	)`)
	if err != nil {
		log.Fatalf("Failed to create users table in PostgreSQL: %v", err)
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS shop (
		id SERIAL PRIMARY KEY,
		product TEXT NOT NULL,
		price INTEGER NOT NULL,
		remains INTEGER,
		rest_number INTEGER
	)`)
	if err != nil {
		log.Fatalf("Failed to create shop table in PostgreSQL: %v", err)
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS orders (
		id SERIAL PRIMARY KEY,
		telegram_id BIGINT NOT NULL,
		product_name TEXT,
		status TEXT,
		product_id  INTEGER,
		rest_number INTEGER,
		price INTEGER,
		created_at  TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP -- TIMESTAMP WITH TIME ZONE предпочтительнее
	)`)
	if err != nil {
		log.Fatalf("Failed to create orders table in PostgreSQL: %v", err)
	}

	// --- Инициализация Telegram бота ---
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Panicf("Failed to create Telegram bot API: %v", err)
	}
	bot.Debug = true // Включите для отладки, выключайте в продакшене

	// --- Настройка команд бота ---
	cmdConfig := tgbotapi.SetMyCommandsConfig{
		Commands: userCommands,
	}
	_, err = bot.Request(cmdConfig)
	if err != nil {
		log.Printf("Error setting bot commands: %v", err)
	} else {
		log.Println("Bot commands set successfully.")
	}

	// --- Получение канала обновлений ---
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60 // Увеличьте таймаут, если есть проблемы с задержками
	updates := bot.GetUpdatesChan(u)

	log.Println("Bot started successfully. Waiting for updates...")

	// Главный цикл получения и обработки событий
	for update := range updates {
		// (1) Регистрация пользователей
		if stepreg.RegistrationHandler(bot, db, update) {
			continue
		}

		//обработка команды меню
		if update.Message != nil && (update.Message.Text == "/menu" || update.Message.Text == "меню") {
			userID := update.Message.From.ID

			features.DeleteAllBotMessages(bot, userID)

			accessLevel, err := database.GetAccessLevel(db, userID)
			if err != nil {
				log.Println("Access Error:", err)
			}

			menuMarkup := features.GenMainMenu(accessLevel, userID, superUser)
			response := tgbotapi.NewMessage(userID, "Ваше меню:")
			response.ReplyMarkup = menuMarkup

			sent, err := bot.Send(response)
			if err == nil {
				features.SentMessages[userID] = []int{sent.MessageID}
			}
			continue
		}

		if update.CallbackQuery != nil {
			callback.HandleCallback(bot, db, update.CallbackQuery, userState, shopState, superUser)
			continue
		}

		if update.Message != nil {
			userID := update.Message.From.ID
			text := update.Message.Text

			if st, ok := shopState[userID]; ok {
				switch st.Field {
				case "wait_new_price", "wait_new_remains", "wait_new_product_name",
					"wait_new_product_price", "wait_new_product_remains":
					log.Println("HandleShopMessage called for user:", userID, "field:", st.Field)
					callback.HandleShopMessage(bot, db, update.Message, shopState)
					continue
				}
			}

			state, ok := userState[userID]

			if ok && (state.Field == "super_user:wait_rest_number" || state.Field == "super_user:wait_access_level") && userID == superUser {
				switch state.Field {
				case "super_user:wait_rest_number":
					restNumber, err := strconv.Atoi(text)
					if err != nil {
						bot.Send(tgbotapi.NewMessage(userID, "❌ Введи корректный номер предприятия (целое число)!"))
					} else {
						err = database.UpdateRest(db, userID, text)
						if err != nil {
							bot.Send(tgbotapi.NewMessage(userID, "Ошибка super_user:transition!"))
						} else {
							bot.Send(tgbotapi.NewMessage(userID, "Номер нового предприятия: "+strconv.Itoa(restNumber)))
						}
						delete(userState, userID)
						continue
					}
				case "super_user:wait_access_level":
					err = database.ChangeAccess(db, userID, text)
					if err != nil {
						bot.Send(tgbotapi.NewMessage(userID, "Ошибка super_user:access!"))
					} else {
						bot.Send(tgbotapi.NewMessage(userID, "Текущий уровень: "+text))
					}
					delete(userState, userID)
					continue
				}
			}

			if ok && state.Field == "wait_table_number" {
				desiredRole := state.Value // worker/manager/admin
				tableNumber := text

				// если роль admin, спрашиваем подтверждение
				if desiredRole == "admin" {

					confirmMarkup := tgbotapi.NewInlineKeyboardMarkup(
						tgbotapi.NewInlineKeyboardRow(
							tgbotapi.NewInlineKeyboardButtonData("✅ Да, я уверен", "confirmAdmin:"+tableNumber),
							tgbotapi.NewInlineKeyboardButtonData("❌ Нет", "cancelAdmin"),
						),
					)
					info := fmt.Sprintf("\nВы выбрали сотрудника с номером %s.\nУверенны, что хотите сделать этого человека администратором?",
						tableNumber)
					msg := tgbotapi.NewMessage(userID, info)
					msg.ReplyMarkup = confirmMarkup
					bot.Send(msg)
					// Можно сохранить tableNumber и роль во временном state
					state.Field = "wait_confirm_admin"
					state.Value = tableNumber
					continue
				}

				//Для worker/manager — сразу применяем
				err := database.ChangeRole(db, userID, tableNumber, desiredRole)
				if err != nil {
					bot.Send(tgbotapi.NewMessage(userID, "❌ Ошибка изменения роли: "+err.Error()))
				} else {
					bot.Send(tgbotapi.NewMessage(userID, "✅ Роль успешно изменена!"))
				}
				delete(userState, userID)
				continue
			}

			if ok && (state.Field == "balance" || state.Field == "name" || state.Field == "tablenumber" || state.Field == "delete") {
				err := database.ApplyCorrection(db, state.ID, state.Field, text)
				if err != nil {
					bot.Send(tgbotapi.NewMessage(userID, "❌ Ошибка корректировки!"))
				} else {
					bot.Send(tgbotapi.NewMessage(userID, "✅ Поле успешно обновлено!"))
				}
				delete(userState, userID) // очищаем состояние
				continue
			}
		}
	}
}
