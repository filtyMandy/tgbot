package callback

import (
	"database/sql"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log"
	"tbViT/database"
)

func handleBuyCallback(bot *tgbotapi.BotAPI, db *sql.DB, cq *tgbotapi.CallbackQuery) {
	buyerID := cq.From.ID
	productID, err := database.ParseProductID(cq.Data)
	if err != nil {
		return
	}
	price, remains, productName, restNum, _ := database.GetPriceRemainsProductName(db, productID)

	ok, err := database.IsSameRest(db, buyerID, productID)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(buyerID, "Ошибка проверки предприятия"))
		answerCallback(bot, cq.ID, "")
		return
	}
	if !ok {
		bot.Send(tgbotapi.NewMessage(buyerID, "⛔ Вы не можете покупать товары другого предприятия!"))
		answerCallback(bot, cq.ID, "")
		return
	}
	if remains < 1 {
		bot.Send(tgbotapi.NewMessage(buyerID, "❗ Товар закончился!"))
		answerCallback(bot, cq.ID, "")
		return
	}

	// --- ТРАНЗАКЦИЯ ---
	tx, err := db.Begin()
	if err != nil {
		bot.Send(tgbotapi.NewMessage(buyerID, "Ошибка транзакции."))
		answerCallback(bot, cq.ID, "")
		return
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	// Проверяем баланс внутри транзакции
	var balance int
	err = tx.QueryRow(`SELECT current_balance FROM users WHERE telegram_id=?`, buyerID).Scan(&balance)
	if err != nil {
		log.Printf("Ошибка получения баланса для %d: %s", buyerID, err)
		bot.Send(tgbotapi.NewMessage(buyerID, "Ошибка загрузки баланса."))
		answerCallback(bot, cq.ID, "")
		return
	}
	if balance < price {
		bot.Send(tgbotapi.NewMessage(buyerID, "Недостаточно средств на балансе!"))
		answerCallback(bot, cq.ID, "")
		return
	}
	// Списание баланса
	_, err = tx.Exec(`UPDATE users SET current_balance = current_balance - ? WHERE telegram_id=?`, price, buyerID)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(buyerID, "Ошибка при оплате."))
		answerCallback(bot, cq.ID, "")
		return
	}

	// Уменьшение остатка
	_, err = tx.Exec(`UPDATE shop SET remains = remains - 1 WHERE id=? AND remains > 0`, productID)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(buyerID, "Ошибка обновления склада."))
		answerCallback(bot, cq.ID, "")
		return
	}
	// Добавляем заказ в orders
	_, err = tx.Exec(`
  INSERT INTO orders (telegram_id, product_name, product_id, status, rest_number, price) VALUES (?, ?, ?, ?, ?, ?)`,
		buyerID, productName, productID, "в сборке", restNum, price,
	)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(buyerID, "Произошла ошибка при оформлении заказа."))
		answerCallback(bot, cq.ID, "")
		tx.Rollback()
		return
	}
	// --- КОММИТ ---
	if err = tx.Commit(); err != nil {
		bot.Send(tgbotapi.NewMessage(buyerID, "Транзакция не завершена."))
		answerCallback(bot, cq.ID, "")
		return
	}

	shopAdmin, err := database.GetAdminID(db, buyerID)

	adminMsg := fmt.Sprintf(
		"🛒 Новый заказ!\nПокупатель: %d\nТовар: %s\nСтатус: В сборке\n\nЧтобы обработать заказы, нажмите «Заказы».",
		buyerID, productName,
	)
	buyerMsg := fmt.Sprintf(
		"Спасибо за покупку!\nВы преобрели: %s.\nПозже, когда можно будет забрать заказ.",
		productName,
	)
	bot.Send(tgbotapi.NewMessage(shopAdmin, adminMsg))
	bot.Send(tgbotapi.NewMessage(buyerID, buyerMsg))
}
