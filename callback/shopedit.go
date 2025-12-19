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

func handleShopEdit(bot *tgbotapi.BotAPI, db *sql.DB, cq *tgbotapi.CallbackQuery, shopState map[int64]*CorrectionState) {
	data := cq.Data
	fromID := cq.From.ID

	switch {
	case data == "shop_edit" && accessLevel == "admin":
		features.ShowShopEdit(bot, fromID)
	case data == "shop_edit:choose" && accessLevel == "admin":
		rows, _ := db.Query(`SELECT id, product, price, remains FROM shop WHERE rest_number=(
			SELECT rest_number FROM users WHERE telegram_id=$1)`, fromID)
		var keyboardRows [][]tgbotapi.InlineKeyboardButton
		var hasItems bool
		for rows.Next() {
			hasItems = true
			var id, price, remains int
			var name string
			rows.Scan(&id, &name, &price, &remains)
			btn := tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("%s (%d🌟, %d шт.)", name, price, remains),
				fmt.Sprintf("shop_edititem:%d", id),
			)
			keyboardRows = append(keyboardRows, tgbotapi.NewInlineKeyboardRow(btn))
		}
		if !hasItems {
			bot.Send(tgbotapi.NewMessage(fromID, "Нет товаров для редактирования."))
			return
		}
		msg := tgbotapi.NewMessage(fromID, "Выберите товар для редактирования:")
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(keyboardRows...)
		bot.Send(msg)

	case strings.HasPrefix(data, "shop_edititem:"):
		id, _ := strconv.Atoi(strings.TrimPrefix(data, "shop_edititem:"))
		shopState[fromID] = &CorrectionState{ID: int64(id), Field: "edit_menu"}
		// Показываем меню для товара
		btns := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("💲 Цена", fmt.Sprintf("shop_editfield:price:%d", id)),
				tgbotapi.NewInlineKeyboardButtonData("📦 Остаток", fmt.Sprintf("shop_editfield:remains:%d", id)),
				tgbotapi.NewInlineKeyboardButtonData("🗑️ Удалить", fmt.Sprintf("shop_editdel:%d", id)),
			),
		)
		msg := tgbotapi.NewMessage(fromID, "Что изменить?")
		msg.ReplyMarkup = btns
		bot.Send(msg)

	case strings.HasPrefix(data, "shop_editfield:"):
		parts := strings.Split(data, ":")
		if len(parts) != 3 {
			bot.Send(tgbotapi.NewMessage(fromID, "Ошибка выбора поля"))
			return
		}
		field, sid := parts[1], parts[2]
		id, _ := strconv.Atoi(sid)
		shopState[fromID] = &CorrectionState{ID: int64(id), Field: "wait_new_" + field}
		_, _, name, _, _ := database.GetPriceRemainsProductName(db, id)
		var msg string
		switch field {
		case "price":
			msg = fmt.Sprintf("Введите новую цену товара(%s):", name)
		case "remains":
			msg = fmt.Sprintf("Введите остаток товара(%s):", name)
		}
		bot.Send(tgbotapi.NewMessage(fromID, msg))

	case strings.HasPrefix(data, "shop_editdel:"):
		id, err := strconv.Atoi(strings.TrimPrefix(data, "shop_editdel:"))
		if err != nil {
			log.Printf("Ошибка конвертации в блоке (shop_editdel)", err)
		}
		err = database.DeleteProduct(db, id)
		if err != nil {
			log.Printf("Ошибка удаления: %w", err)
			bot.Send(tgbotapi.NewMessage(fromID, "❌ Ошибка удаления товара"))
		} else {
			bot.Send(tgbotapi.NewMessage(fromID, "✅ Товар удалён"))
		}
		delete(shopState, fromID)

	case data == "shop_edit:shop_add":
		shopState[fromID] = &CorrectionState{Field: "wait_new_product_name"}
		bot.Send(tgbotapi.NewMessage(fromID, "Введите название нового товара:"))
	}
}

func HandleShopMessage(bot *tgbotapi.BotAPI, db *sql.DB, msg *tgbotapi.Message, shopState map[int64]*CorrectionState) {
	fromID := msg.From.ID
	st, ok := shopState[fromID]
	if !ok {
		return
	}

	switch st.Field {
	// --- редактирование ---
	case "wait_new_price":
		price, err := strconv.Atoi(msg.Text)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(fromID, "Вводите только число!⛔️"))
			return
		}
		if price < 0 {
			bot.Send(tgbotapi.NewMessage(fromID, "Цена не может быть отрицательной!⛔️"))
			return
		}

		_, err = db.Exec("UPDATE shop SET price=$1 WHERE id=$2", price, st.ID)
		if err == nil {
			bot.Send(tgbotapi.NewMessage(fromID, "✅ Цена обновлена!"))
		} else {
			bot.Send(tgbotapi.NewMessage(fromID, "❌ Не удалось обновить"))
		}
		delete(shopState, fromID)

	case "wait_new_remains":
		remains, err := strconv.Atoi(msg.Text)
		if err != nil {
			log.Println("Ошибка парсинга remains:", err)
			bot.Send(tgbotapi.NewMessage(fromID, "Вводите только число!"))
			return
		}
		_, err = db.Exec("UPDATE shop SET remains=$1 WHERE id=$2", remains, st.ID)
		if err == nil {
			log.Println("Ошибка UPDATE remains:", err)
			bot.Send(tgbotapi.NewMessage(fromID, "✅ Остаток обновлён!"))
		} else {
			bot.Send(tgbotapi.NewMessage(fromID, "❌ Не удалось обновить"))
		}
		delete(shopState, fromID)

	// --- добавление ---
	case "wait_new_product_name":
		st.Value = msg.Text
		st.Field = "wait_new_product_price"
		shopState[fromID] = st
		bot.Send(tgbotapi.NewMessage(fromID, "Введите цену товара:"))

	case "wait_new_product_price":
		price, err := strconv.Atoi(msg.Text)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(fromID, "Вводите только число!⛔️"))
			return
		}
		if price < 0 {
			bot.Send(tgbotapi.NewMessage(fromID, "Цена не может быть отрицательной!⛔️"))
			return
		}
		st.Field = "wait_new_product_remains"
		st.Value = fmt.Sprintf("%s|%d", st.Value, price)
		shopState[fromID] = st
		bot.Send(tgbotapi.NewMessage(fromID, "Введите количество товара:"))

	case "wait_new_product_remains":
		remains, err := strconv.Atoi(msg.Text)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(fromID, "Вводите только число!"))
			return
		}
		// value = "название|цена"
		parts := strings.Split(st.Value, "|")
		name := parts[0]
		restNum, err := database.GetUserRestID(db, fromID)
		if err != nil {
			log.Println("ошибка получения номера ресторана при добавлении товара", err)
		}
		price, _ := strconv.Atoi(parts[1])
		_, err = db.Exec("INSERT INTO shop (product, price, remains, rest_number) VALUES ($1, $2, $3, $4)",
			name, price, remains, restNum)
		if err == nil {
			bot.Send(tgbotapi.NewMessage(fromID, "✅ Товар добавлен!"))
		} else {
			bot.Send(tgbotapi.NewMessage(fromID, "❌ Ошибка добавления"))
		}
		delete(shopState, fromID)
	}
}
