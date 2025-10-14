package features

import (
	"database/sql"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log"
	"tbViT/database"
)

var SentMessages = make(map[int64][]int)

func ShowShop(bot *tgbotapi.BotAPI, db *sql.DB, chatID int64, userID int64) {
	restID, _ := database.GetUserRestID(db, userID)
	rows, err := db.Query(`SELECT id, product, price, remains FROM shop WHERE remains > 0 AND rest_number=?`, restID)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, "Ошибка чтения магазина"))
		return
	}
	defer rows.Close()
	var kbRows [][]tgbotapi.InlineKeyboardButton
	text := "🛒 Доступные товары:\n"
	for rows.Next() {
		var id int
		var product string
		var price, remains int
		rows.Scan(&id, &product, &price, &remains)
		text += fmt.Sprintf("• %s — %d🌟 (%d шт.)\n", product, price, remains)
		btn := tgbotapi.NewInlineKeyboardButtonData(
			fmt.Sprintf("Купить %s (%d🌟)", product, price),
			fmt.Sprintf("buy_product:%d", id),
		)
		kbRows = append(kbRows, tgbotapi.NewInlineKeyboardRow(btn))
	}
	if len(kbRows) == 0 {
		text = "😔 Товары закончились!"
	}
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(kbRows...)
	bot.Send(msg)
}

func ShowShopEdit(bot *tgbotapi.BotAPI, adminID int64) {
	buttons := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📝 Отредактировать товар", "shop_edit:choose"),
			tgbotapi.NewInlineKeyboardButtonData("➕ Добавить товар", "shop_edit:shop_add"),
		),
	)
	msg := tgbotapi.NewMessage(adminID, "Меню магазина:")
	msg.ReplyMarkup = buttons
	bot.Send(msg)
}

func ShowOrders(bot *tgbotapi.BotAPI, db *sql.DB, fromID int64) {
	buttons, _ := database.KeyboardOrders(db, fromID)
	msg := tgbotapi.NewMessage(fromID, "Активные заказы:")
	msg.ReplyMarkup = buttons
	if len(buttons.InlineKeyboard) == 0 {
		msg = tgbotapi.NewMessage(fromID, "В предприятии отсутствуют заказы")
	}
	bot.Send(msg)
}

func AcceptOrders(bot *tgbotapi.BotAPI, db *sql.DB, fromID int64, orderID int) {
	accept := fmt.Sprintf("orders_order:%d:accept", orderID)
	deny := fmt.Sprintf("orders_order:%d:deny", orderID)
	buttons := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Выполнить", accept),
			tgbotapi.NewInlineKeyboardButtonData("❌ Отменить", deny),
		),
	)
	num, name, product, price, err := database.GetOrderInfo(db, orderID)
	if err != nil {
		log.Printf("Ошибка получения информации о заказе и покупателе: %v", err)
	}
	text := fmt.Sprintf("Заказ:\n%s %s\n%s %d🌟", num, name, product, price)
	msg := tgbotapi.NewMessage(fromID, text)
	msg.ReplyMarkup = buttons
	bot.Send(msg)
}

func CompliteOrders(bot *tgbotapi.BotAPI, db *sql.DB, fromID int64, orderID int, decision string) {
	if decision == "accept" {
		buyerID, product := database.CompleteOrder(db, orderID, decision)
		if product == "complite" {
			msgAdmin := tgbotapi.NewMessage(fromID, "Заказ уже был обработан! ⛔️")
			bot.Send(msgAdmin)
		} else {
			msgBuyer := fmt.Sprintf("Заказ (%s) доставлен в офис.\nМожно забирать.", product)
			msg := tgbotapi.NewMessage(buyerID, msgBuyer)
			msgAdmin := tgbotapi.NewMessage(fromID, "Покупатель уведомлен о готовности заказа.✅")
			bot.Send(msg)
			bot.Send(msgAdmin)
		}
	}
	if decision == "deny" {
		buyerID, product := database.CompleteOrder(db, orderID, decision)
		msgBuyer := fmt.Sprintf("Заказа (%s) отменен.\nПодробности у администратора магазина.", product)
		msg := tgbotapi.NewMessage(buyerID, msgBuyer)
		bot.Send(msg)
		msgAdmin := tgbotapi.NewMessage(fromID, "Покупатель уведомлен об отмене заказа.❌")
		bot.Send(msgAdmin)
	}
}

func DeleteAllBotMessages(bot *tgbotapi.BotAPI, chatID int64) {
	for _, mID := range SentMessages[chatID] {
		del := tgbotapi.DeleteMessageConfig{
			ChatID:    chatID,
			MessageID: mID,
		}
		_, err := bot.Request(del)
		log.Printf("deleteMessage chat=%d msg=%d err=%v", chatID, mID, err)
	}
	SentMessages[chatID] = nil
}
