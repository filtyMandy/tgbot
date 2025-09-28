package database

import (
	"database/sql"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log"
	"strconv"
	"strings"
	"time"
)

func SendHistoryOrders(db *sql.DB, fromID int64) (string, error) {
	rows, err := db.Query(`SELECT product_name, status, price, created_at
FROM orders WHERE telegram_id=? ORDER BY created_at DESC`, fromID)
	if err != nil {
		log.Printf("Ошибка загрузки списка заказов: %v", err)
		return "", err
	}
	defer rows.Close()

	var list strings.Builder
	for rows.Next() {
		var product, status, price string
		var createdAt time.Time
		if err := rows.Scan(&product, &status, &price, &createdAt); err != nil {
			log.Printf("Ошибка скана в SendHistoryOrders: %v", err)
			continue
		}
		var msg string
		dateOnly := createdAt.Format("2006-01-02")
		switch status {
		case "deny":
			msg = "Отменен ❌"
		case "в сборке":
			msg = "В сборке 🚚"
		case "accept":
			msg = "Выполнен ✅"
		}
		list.WriteString(fmt.Sprintf("%s | %s | %s | %s🌟\n", dateOnly, product, msg, price))
	}

	if err = rows.Err(); err != nil {
		log.Printf("Ошибка при итерации по строкам: %v", err)
		return "", err
	}
	return list.String(), nil
}

func CompleteOrder(db *sql.DB, id int, decision string) (int64, string) {
	var buyerID int64
	var price int
	var product, status string
	err := db.QueryRow(`SELECT telegram_id, price, product_name, status FROM orders WHERE id = ?`,
		id).Scan(&buyerID, &price, &product, &status)
	if status == "deny" || status == "accept" {
		return buyerID, "complite"
	}

	if err != nil {
		log.Printf("Ошибка получения данных при возврате средств: %v", err)
	}

	if decision == "deny" {
		_, err = db.Exec("UPDATE users SET current_balance = current_balance + ? WHERE telegram_id = ?", price, buyerID)
		if err != nil {
			log.Printf("Ошибка возврата баланса пользователю %d: %v", buyerID, err)
		}
	}

	db.Exec("UPDATE orders SET status=? WHERE id=?", decision, id)

	return buyerID, product
}

func GetOrderInfo(db *sql.DB, id int) (string, string, string, int, error) {
	var buyerID int64
	var product, num, name string
	var price int

	err := db.QueryRow(`SELECT telegram_id, product_name, price FROM orders WHERE id = ?`,
		id).Scan(&buyerID, &product, &price)
	if err != nil {
		log.Printf("Ошибка получения данных заказа: %v", err)
		return "", "", "", 0, err
	}

	err = db.QueryRow(`SELECT table_number, name FROM users WHERE telegram_id = ?`, buyerID).Scan(&num, &name)
	if err != nil {
		log.Printf("Ошибка получения данных покупателя: %v", err)
		return "", "", "", 0, err
	}

	return num, name, product, price, nil
}

func DeleteProduct(db *sql.DB, id int) error {
	_, err := db.Exec("DELETE FROM shop WHERE id=?", id)
	if err != nil {
		return err
	}
	return nil
}

func GetUserRestID(db *sql.DB, userID int64) (int, error) {
	var restID int
	err := db.QueryRow(`SELECT rest_number FROM users WHERE telegram_id=?`, userID).Scan(&restID)
	return restID, err
}

func GetProductRestID(db *sql.DB, productID int) (int, error) {
	var restID int
	err := db.QueryRow(`SELECT rest_number FROM shop WHERE id=?`, productID).Scan(&restID)
	return restID, err
}

func GetPriceRemainsProductName(db *sql.DB, productID int) (int, int, string, int, error) {
	var price, remains, restNum int
	var productName string
	err := db.QueryRow(`SELECT price, remains, product, rest_number FROM shop WHERE id=?`, productID).Scan(&price, &remains, &productName, &restNum)
	if err != nil {
		return 0, 0, "", 0, fmt.Errorf("Ошибка при получении стоимости товара: %w", err)
	}
	return price, remains, productName, restNum, nil
}

func IsSameRest(db *sql.DB, userID int64, productID int) (bool, error) {
	userDepID, err := GetUserRestID(db, userID)
	if err != nil {
		return false, err
	}
	productDepID, err := GetProductRestID(db, productID)
	if err != nil {
		return false, err
	}
	return userDepID == productDepID, nil
}

func ParseProductID(data string) (int, error) {
	idStr := strings.TrimPrefix(data, "buy_product:")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func KeyboardOrders(db *sql.DB, fromID int64) (tgbotapi.InlineKeyboardMarkup, string) {
	rows, err := db.Query(`SELECT id, product_name, telegram_id FROM orders WHERE rest_number=(
			SELECT rest_number FROM users WHERE telegram_id=?) AND status = ?`, fromID, "в сборке")
	if err != nil {
		log.Printf("Ошибка запроса KeyboardOrders: %v", err)
		return tgbotapi.NewInlineKeyboardMarkup([][]tgbotapi.InlineKeyboardButton{}...), "Ошибка загрузки заказов"
	}
	defer rows.Close()

	var keyboardRows [][]tgbotapi.InlineKeyboardButton
	for rows.Next() {
		var userID int64
		var id int
		var product string
		if err := rows.Scan(&id, &product, &userID); err != nil {
			log.Printf("Ошибка скана KeyboardOrders: %v", err)
			continue
		}
		num, name, _, _, _ := GetWorkerInfoValues(db, userID)
		btn := tgbotapi.NewInlineKeyboardButtonData(
			fmt.Sprintf("%s %s (%s)", num, name, product),
			fmt.Sprintf("orders_order:%d", id),
		)
		keyboardRows = append(keyboardRows, tgbotapi.NewInlineKeyboardRow(btn))
	}
	if len(keyboardRows) == 0 {
		return tgbotapi.InlineKeyboardMarkup{}, "В предприятии отсутствуют заказы"
	}

	return tgbotapi.NewInlineKeyboardMarkup(keyboardRows...), ""
}
