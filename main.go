package main

import (
	"database/sql"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	"log"
	_ "modernc.org/sqlite"
	"os"
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

var userState = make(map[int64]*callback.CorrectionState)
var shopState = make(map[int64]*callback.CorrectionState)

func main() {

	err := godotenv.Load()
	botToken := os.Getenv("TOCKEN")
	if botToken == "" {
		panic("Missing token")
	}
	db, err := sql.Open("sqlite", "botdata.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Создаём таблицу
	db.Exec(`CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		telegram_id INTEGER UNIQUE NOT NULL,
		username TEXT,
		name TEXT,
		table_number TEXT,
		rest_number INTEGER,
		access_level TEXT, 
		verified INTEGER,
		reg_state TEXT,
        current_balance INTEGER,
        all_time_balance INTEGER,
        last_ts INTEGER,
        tmp_field INTEGER,
        special_roll TEXT                         
	)`)

	db.Exec(`CREATE TABLE IF NOT EXISTS shop (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		product TEXT NOT NULL,
		price INTEGER NOT NULL,
		remains INTEGER,
		rest_number INTEGER                        
	)`)

	db.Exec(`CREATE TABLE IF NOT EXISTS orders (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		telegram_id INTEGER NOT NULL,
		product_name TEXT,  
		status TEXT, 
		product_id  INTEGER, 
		rest_number INTEGER ,
		price INTEGER,  
		created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP           
	)`)

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Panic(err)
	}
	bot.Debug = true

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	_, err = bot.Request(tgbotapi.NewSetMyCommands(userCommands...))
	if err != nil {
		log.Println("Ошибка установки меню user:", err)
	}

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

			menuMarkup := features.GenMainMenu(accessLevel)
			response := tgbotapi.NewMessage(userID, "Ваше меню:")
			response.ReplyMarkup = menuMarkup

			sent, err := bot.Send(response)
			if err == nil {
				features.SentMessages[userID] = []int{sent.MessageID}
			}
			continue
		}

		if update.CallbackQuery != nil {
			callback.HandleCallback(bot, db, update.CallbackQuery, userState, shopState)
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

				// 👷‍♂️ Для worker/manager — сразу применяем
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
