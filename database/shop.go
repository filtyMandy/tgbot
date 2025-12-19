package database

import (
	"database/sql"
	"errors"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log"
	"strconv"
	"strings"
	"time"
)

func SendHistoryOrders(db *sql.DB, fromID int64) (string, error) {
	// PostgreSQL: WHERE telegram_id = $1
	query := `SELECT product_name, status, price, created_at
              FROM orders
              WHERE telegram_id = $1
              ORDER BY created_at DESC`
	rows, err := db.Query(query, fromID)
	if err != nil {
		log.Printf("Ошибка загрузки списка заказов для %d: %v", fromID, err)
		return "", fmt.Errorf("при загрузке списка заказов для %d: %w", fromID, err)
	}
	defer rows.Close()

	var list strings.Builder
	for rows.Next() {
		var product, status, price string
		var createdAt time.Time
		// Scan работает одинаково
		if err := rows.Scan(&product, &status, &price, &createdAt); err != nil {
			log.Printf("Ошибка сканирования строки заказа для %d: %v", fromID, err)
			continue // Пропускаем некорректную строку
		}
		var msg string
		// Форматирование даты остается прежним
		dateOnly := createdAt.Format("2006-01-02")
		switch status {
		case "deny":
			msg = "Отменен ❌"
		case "в сборке": // Предполагается, что это текстовое значение
			msg = "В сборке 🚚"
		case "accept":
			msg = "Выполнен ✅"
		default: // Обработка неизвестного статуса, если потребуется
			msg = status
		}
		// Форматирование строки вывода
		list.WriteString(fmt.Sprintf("%s | %s | %s | %s🌟\n", dateOnly, product, msg, price))
	}

	if err = rows.Err(); err != nil {
		log.Printf("Ошибка итерации по строкам заказов для %d: %v", fromID, err)
		return "", fmt.Errorf("при итерации по строкам заказов для %d: %w", fromID, err)
	}
	return list.String(), nil
}

// CompleteOrder обрабатывает решение по заказу (принять или отклонить).
// Возвращает telegram_id покупателя и название продукта/статус.
func CompleteOrder(db *sql.DB, id int, decision string) (int64, string, error) {
	var buyerID int64
	var price int
	var product, status string

	// PostgreSQL: SELECT ... WHERE id = $1
	query := `SELECT telegram_id, price, product_name, status FROM orders WHERE id = $1`
	err := db.QueryRow(query, id).Scan(&buyerID, &price, &product, &status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Printf("Заказ с ID %d не найден", id)
			return 0, "", fmt.Errorf("заказ с ID %d не найден", id)
		}
		log.Printf("Ошибка получения данных заказа %d: %v", id, err)
		return 0, "", fmt.Errorf("при получении данных заказа %d: %w", id, err)
	}

	// Если заказ уже завершен, ничего не делаем
	if status == "deny" || status == "accept" {
		log.Printf("Заказ %d уже имеет статус %s", id, status)
		return buyerID, "completed", nil // Возвращаем статус, что уже обработан
	}

	// Если решение - "deny" (отклонить), возвращаем средства
	if decision == "deny" {
		// PostgreSQL: UPDATE users SET current_balance = current_balance + $1 WHERE telegram_id = $2
		returnBalanceQuery := `UPDATE users SET current_balance = current_balance + $1 WHERE telegram_id = $2`
		_, err = db.Exec(returnBalanceQuery, price, buyerID)
		if err != nil {
			log.Printf("Ошибка возврата баланса пользователю %d (заказ %d): %v", buyerID, id, err)
			return buyerID, product, fmt.Errorf("при возврате баланса пользователю %d: %w", buyerID, err)
		}
		log.Printf("Баланс %d возвращен пользователю %d за заказ %d", price, buyerID, id)
	}

	// Обновляем статус заказа
	// PostgreSQL: UPDATE orders SET status = $1 WHERE id = $2
	updateStatusQuery := `UPDATE orders SET status = $1 WHERE id = $2`
	_, err = db.Exec(updateStatusQuery, decision, id)
	if err != nil {
		log.Printf("Ошибка обновления статуса заказа %d на %s: %v", id, decision, err)
		return buyerID, product, fmt.Errorf("при обновлении статуса заказа %d: %w", id, err)
	}

	log.Printf("Статус заказа %d изменен на %s", id, decision)
	return buyerID, product, nil // Успешное завершение
}

// GetOrderInfo получает основную информацию о заказе и покупателе.
func GetOrderInfo(db *sql.DB, id int) (string, string, string, int, error) {
	var buyerID int64
	var product, num, name string
	var price int

	// PostgreSQL: SELECT ... WHERE id = $1
	orderQuery := `SELECT telegram_id, product_name, price FROM orders WHERE id = $1`
	err := db.QueryRow(orderQuery, id).Scan(&buyerID, &product, &price)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", "", 0, fmt.Errorf("заказ с ID %d не найден", id)
		}
		log.Printf("Ошибка получения данных заказа %d: %v", id, err)
		return "", "", "", 0, fmt.Errorf("при получении данных заказа %d: %w", id, err)
	}

	// PostgreSQL: SELECT ... WHERE telegram_id = $1
	userQuery := `SELECT table_number, name FROM users WHERE telegram_id = $1`
	err = db.QueryRow(userQuery, buyerID).Scan(&num, &name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Printf("Покупатель (telegram_id %d) заказа %d не найден", buyerID, id)
			return "", "", "", 0, fmt.Errorf("покупатель (telegram_id %d) заказа %d не найден", buyerID, id)
		}
		log.Printf("Ошибка получения данных покупателя %d для заказа %d: %v", buyerID, id, err)
		return "", "", "", 0, fmt.Errorf("при получении данных покупателя %d: %w", buyerID, err)
	}

	return num, name, product, price, nil
}

// DeleteProduct удаляет товар из магазина.
func DeleteProduct(db *sql.DB, id int) error {
	// PostgreSQL: DELETE FROM shop WHERE id = $1
	_, err := db.Exec("DELETE FROM shop WHERE id = $1", id)
	if err != nil {
		log.Printf("Ошибка при удалении товара %d: %v", id, err)
		return fmt.Errorf("при удалении товара %d: %w", id, err)
	}
	log.Printf("Товар %d успешно удален", id)
	return nil
}

// GetUserRestID получает rest_number пользователя.
func GetUserRestID(db *sql.DB, userID int64) (int, error) {
	var restID int
	// PostgreSQL: SELECT rest_number FROM users WHERE telegram_id = $1
	query := `SELECT rest_number FROM users WHERE telegram_id = $1`
	err := db.QueryRow(query, userID).Scan(&restID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("пользователь с telegram_id=%d не найден", userID)
		}
		log.Printf("Ошибка получения rest_number для %d: %v", userID, err)
		return 0, fmt.Errorf("при получении rest_number для %d: %w", userID, err)
	}
	return restID, nil
}

// GetProductRestID получает rest_number товара.
func GetProductRestID(db *sql.DB, productID int) (int, error) {
	var restID int
	// PostgreSQL: SELECT rest_number FROM shop WHERE id = $1
	query := `SELECT rest_number FROM shop WHERE id = $1`
	err := db.QueryRow(query, productID).Scan(&restID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("товар с ID %d не найден", productID)
		}
		log.Printf("Ошибка получения rest_number для товара %d: %v", productID, err)
		return 0, fmt.Errorf("при получении rest_number для товара %d: %w", productID, err)
	}
	return restID, nil
}

// GetPriceRemainsProductName получает цену, остаток, название товара и rest_number.
func GetPriceRemainsProductName(db *sql.DB, productID int) (price int, remains int, productName string, restNum int, err error) {
	// PostgreSQL: SELECT price, remains, product, rest_number FROM shop WHERE id = $1
	query := `SELECT price, remains, product, rest_number FROM shop WHERE id = $1`
	err = db.QueryRow(query, productID).Scan(&price, &remains, &productName, &restNum)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, "", 0, fmt.Errorf("товар с ID %d не найден", productID)
		}
		log.Printf("Ошибка получения данных товара %d: %v", productID, err)
		return 0, 0, "", 0, fmt.Errorf("при получении данных товара %d: %w", productID, err)
	}
	return price, remains, productName, restNum, nil
}

// IsSameRest проверяет, принадлежит ли пользователь и товар одному предприятию (rest_number).
func IsSameRest(db *sql.DB, userID int64, productID int) (bool, error) {
	userDepID, err := GetUserRestID(db, userID)
	if err != nil {
		return false, fmt.Errorf("при проверке предприятия пользователя: %w", err)
	}
	productDepID, err := GetProductRestID(db, productID)
	if err != nil {
		return false, fmt.Errorf("при проверке предприятия товара: %w", err)
	}
	return userDepID == productDepID, nil
}

// ParseProductID извлекает ID товара из строки с префиксом.
func ParseProductID(data string) (int, error) {
	// TrimPrefix работает одинаково
	idStr := strings.TrimPrefix(data, "buy_product:")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		log.Printf("Ошибка преобразования ID товара из '%s': %v", idStr, err)
		return 0, fmt.Errorf("некорректный формат ID товара '%s': %w", idStr, err)
	}
	return id, nil
}

// KeyboardOrders генерирует клавиатуру с заказами для предприятия.
func KeyboardOrders(db *sql.DB, fromID int64) (tgbotapi.InlineKeyboardMarkup, string) {
	query := `SELECT id, product_name, telegram_id
              FROM orders
              WHERE rest_number = (
                  SELECT rest_number FROM users WHERE telegram_id = $1
              ) AND status = $2
              ORDER BY created_at DESC` // Добавим сортировку для консистентности
	rows, err := db.Query(query, fromID, "в сборке")
	if err != nil {
		log.Printf("Ошибка запроса KeyboardOrders для %d: %v", fromID, err)
		return tgbotapi.NewInlineKeyboardMarkup(), "Ошибка загрузки заказов" // Возвращаем пустую клавиатуру
	}
	defer rows.Close()

	var keyboardRows [][]tgbotapi.InlineKeyboardButton
	for rows.Next() {
		var userID int64
		var id int
		var product string
		if err := rows.Scan(&id, &product, &userID); err != nil {
			log.Printf("Ошибка сканирования строки в KeyboardOrders для %d: %v", fromID, err)
			continue // Пропускаем некорректную строку
		}

		// Получаем информацию о покупателе
		num, name, _, _, err := GetWorkerInfoValues(db, userID)
		if err != nil {
			log.Printf("Не удалось получить информацию о покупателе %d для заказа %d: %v", userID, id, err)
			// Можно пропустить этот заказ или вывести сообщение об ошибке
			continue
		}

		btnText := fmt.Sprintf("%s %s (%s)", num, name, product)
		callbackData := fmt.Sprintf("orders_order:%d", id)
		btn := tgbotapi.NewInlineKeyboardButtonData(btnText, callbackData)
		keyboardRows = append(keyboardRows, tgbotapi.NewInlineKeyboardRow(btn))
	}

	if len(keyboardRows) == 0 {
		return tgbotapi.NewInlineKeyboardMarkup(), "В предприятии отсутствуют заказы в сборке"
	}

	return tgbotapi.NewInlineKeyboardMarkup(keyboardRows...), ""
}
