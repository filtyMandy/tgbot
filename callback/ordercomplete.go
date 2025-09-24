package callback

import (
	"database/sql"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log"
	"strconv"
	"strings"
	"tbViT/features"
)

func handleOrderComplete(bot *tgbotapi.BotAPI, db *sql.DB, cq *tgbotapi.CallbackQuery) {
	data := cq.Data
	fromID := cq.From.ID

	switch {
	case data == "orders" && accessLevel == "admin":
		features.ShowOrders(bot, db, fromID)

	case strings.HasPrefix(data, "orders_order") && accessLevel == "admin":
		parts := strings.Split(data, ":")
		orderID, err := strconv.Atoi(parts[1])
		if err != nil {
			log.Printf("Ошибка получения ID заказа: %v", err)
		}

		if len(parts) == 2 {
			features.AcceptOrders(bot, db, fromID, orderID)
		}
		if len(parts) == 3 {
			decision := parts[2]
			features.CompliteOrders(bot, db, fromID, orderID, decision)
		}
	}
}
