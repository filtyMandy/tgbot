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

func DeleteUser(db *sql.DB, telegramID int64) error {
	_, err := db.Exec("DELETE FROM users WHERE telegram_id = ?", telegramID)
	return err
}

func SendWorkersString(db *sql.DB, fromID int64) (string, error) {
	restNum, err := SameRest(db, fromID)
	if err != nil {
		log.Printf("Ошибка получения rest_number: %v", err)
		return "", err
	}
	log.Printf("rest_number для %d: %d", fromID, restNum)

	rows, err := db.Query(`SELECT table_number, name, access_level, current_balance
FROM users WHERE rest_number=? ORDER BY CAST(table_number AS INTEGER) ASC`, int(restNum))
	if err != nil {
		log.Printf("Ошибка загрузки списка сотрудников: %v", err)
		return "", err
	}
	defer rows.Close()

	var list strings.Builder
	for rows.Next() {
		var num, name, access string
		var balance int
		if err := rows.Scan(&num, &name, &access, &balance); err != nil {
			log.Printf("Ошибка скана в SendWorkersString: %v", err)
			continue
		}
		list.WriteString(fmt.Sprintf("%s %s|%s|%d🌟\n", num, name, access, balance))
	}

	if err = rows.Err(); err != nil {
		log.Printf("Ошибка при итерации по строкам: %v", err)
		return "", err
	}
	return list.String(), nil
}

func ChangeRole(db *sql.DB, oldAdminID int64, tableNumber, role string) error {
	// Найти пользователя по номеру расписания и тому же предприятию
	var newUserID int64
	err := db.QueryRow(
		`SELECT telegram_id FROM users WHERE table_number=? AND rest_number=(
            SELECT rest_number FROM users WHERE telegram_id=?
        ) LIMIT 1`, tableNumber, oldAdminID,
	).Scan(&newUserID)
	if err == sql.ErrNoRows {
		return errors.New("Пользователь не найден")
	}
	if err != nil {
		return err
	}
	// Применить роль
	_, err = db.Exec(`UPDATE users SET access_level=? WHERE telegram_id=?`, role, newUserID)
	if err != nil {
		return err
	}
	// Если роль — admin, то понижаем старого админа
	if role == "admin" {
		_, err = db.Exec(`UPDATE users SET access_level='manager' WHERE telegram_id=?`, oldAdminID)
		if err != nil {
			return err
		}
	}
	return nil
}

func GetWorkerInfoValues(db *sql.DB, workerID int64) (string, string, string, int, error) {
	var access, name, tableNumber string
	var balance int
	err := db.QueryRow(`
        SELECT table_number, name, access_level, current_balance
        FROM users
        WHERE telegram_id = ?`, workerID).Scan(&tableNumber, &name, &access, &balance)
	if err != nil {
		log.Printf("ошибка получения информации о сотруднике: %v", err)
		return "", "", "", 0, err
	}
	return tableNumber, name, access, balance, nil
}

func GetWorkerInfo(db *sql.DB, workerID int64) string {
	var access, name string
	var num, balance int
	_ = db.QueryRow("SELECT table_number, access_level, current_balance, name from users where telegram_id=?",
		workerID).Scan(&num, &access, &balance, &name)
	return fmt.Sprintf("Вы выбрали %d %s\nУровень доступа: %s\nТекущий баланс: %d",
		num, name, access, balance)
}

func ApplyCorrection(db *sql.DB, workerID int64, field, value string) error {

	if field == "delete" {
		// Проверим, что value == "true" или "1" (опционально, для безопасности)
		// Можно также просто игнорировать value и удалять по workerID
		_, err := db.Exec("DELETE FROM users WHERE telegram_id = ?", workerID)
		if err != nil {
			return errors.New("не удалось удалить пользователя")
		}
		return nil
	}

	var query string

	switch field {
	case "balance", "tablenumber":
		n, err := strconv.Atoi(value)
		if err != nil {
			return errors.New("значение должно быть целым числом")
		}
		if n < 0 {
			return errors.New("значение должно быть положительным числом или нулём")
		}
	}

	switch field {
	case "balance":
		query = "UPDATE users SET current_balance=? WHERE telegram_id=?"
	case "name":
		query = "UPDATE users SET name=? WHERE telegram_id=?"
	case "tablenumber":
		query = "UPDATE users SET table_number=? WHERE telegram_id=?"
	default:
		return errors.New("unknown field")
	}
	_, err := db.Exec(query, value, workerID)
	return err
}

func GetAccessLevel(db *sql.DB, userID int64) (string, error) {
	var accessLevel string
	err := db.QueryRow("SELECT access_level FROM users WHERE telegram_id=?", userID).Scan(&accessLevel)
	if err != nil {
		return "", err
	}
	return accessLevel, nil
}

func GetUserDep(db *sql.DB, telegramID int64) (string, error) {
	var dep string
	err := db.QueryRow(
		`SELECT rest_number FROM users WHERE telegram_id=?`,
		telegramID,
	).Scan(&dep)
	return dep, err
}

func SendWorkersList(bot *tgbotapi.BotAPI, db *sql.DB, chatID int64, status string, dep string, page int) error {
	const pageSize = 15

	// Считаем общее количество работников
	var total int
	err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE rest_number=? AND access_level='worker' AND verified=1`, dep).Scan(&total)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, "Ошибка получения количества работников."))
		return err
	}

	offset := page * pageSize

	rows, err := db.Query(
		`SELECT telegram_id, name, table_number
         FROM users
         WHERE rest_number=? AND access_level='worker' AND verified=1
         ORDER BY CAST(table_number AS INTEGER) ASC
         LIMIT ? OFFSET ?`, dep, pageSize, offset,
	)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, "Ошибка получения работников."))
		return err
	}
	defer rows.Close()

	buttons := [][]tgbotapi.InlineKeyboardButton{}
	for rows.Next() {
		var workerID int64
		var name, tableNum string
		if err := rows.Scan(&workerID, &name, &tableNum); err != nil {
			continue
		}
		btnText := fmt.Sprintf("%s %s", tableNum, name)
		callbackData := fmt.Sprintf("%s:%d", status, workerID)
		buttons = append(buttons, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(btnText, callbackData),
		))
	}
	if len(buttons) == 0 {
		bot.Send(tgbotapi.NewMessage(chatID, "❌ В вашем предприятии нет работников."))
		return nil
	}

	// Кнопки пагинации:
	paginationButtons := []tgbotapi.InlineKeyboardButton{}
	if page > 0 {
		paginationButtons = append(paginationButtons,
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад", fmt.Sprintf("select_worker:%d:%s:%s", page-1, status, dep)),
		)
	}
	if offset+pageSize < total {
		paginationButtons = append(paginationButtons,
			tgbotapi.NewInlineKeyboardButtonData("➡️ Дальше", fmt.Sprintf("select_worker:%d:%s:%s", page+1, status, dep)),
		)
	}
	if len(paginationButtons) > 0 {
		buttons = append(buttons, paginationButtons)
	}

	replyMsg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Выберите работника (страница %d):", page+1))
	replyMsg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(buttons...)
	bot.Send(replyMsg)
	return nil
}

func GetAdminID(db *sql.DB, userID int64) (int64, error) {
	var adminTelegramID int64
	rn, err := SameRest(db, userID)
	err = db.QueryRow(
		`SELECT telegram_id FROM users WHERE rest_number=? AND access_level='admin' LIMIT 1`, rn,
	).Scan(&adminTelegramID)
	if err != nil {
		log.Println("Нет админа для предприятия:", rn, err)
	}
	return adminTelegramID, nil
}

func GetBalance(db *sql.DB, userID int64) (int, error) {
	var balance int
	err := db.QueryRow("SELECT current_balance FROM users WHERE telegram_id=?",
		userID).Scan(&balance)
	if err != nil {
		return 0, err
	}
	return balance, nil
}

func SameRest(db *sql.DB, userID int64) (int64, error) {
	var rn int64
	err := db.QueryRow("SELECT rest_number FROM users WHERE telegram_id=?",
		userID).Scan(&rn)
	if err != nil {
		return 0, err
	}
	return rn, nil
}

func CanManagerChangeBalance(db *sql.DB, workerID int64) (bool, string) {
	var lastTs int64
	err := db.QueryRow("SELECT last_ts FROM users WHERE telegram_id=?",
		workerID).Scan(&lastTs)
	if err != nil && err != sql.ErrNoRows {
		return false, "DB error"
	}
	if lastTs == 0 {
		return true, ""
	}
	epl := time.Now().Unix() - lastTs
	if epl < 12*3600 {
		left := 12*3600 - epl
		hours := left / 3600
		mins := (left % 3600) / 60
		return false, fmt.Sprintf("❗ Cooldown: %d housrs  %d mins", hours, mins)
	}
	return true, ""
}

func TopUpBalance(db *sql.DB, workerID int64, amount int) (string, bool, error) {
	ok, msg := CanManagerChangeBalance(db, workerID)
	if !ok {
		return msg, false, nil
	} else {
		tx, err := db.Begin()
		if err != nil {
			return "Miss begin transaction", false, err
		}
		//rising balance
		_, err = tx.Exec("UPDATE users SET current_balance = current_balance + ? WHERE telegram_id=?",
			amount, workerID)
		if err != nil {
			tx.Rollback()
			return "Err updating balance", false, err
		}
		//buf time operation
		now := time.Now().Unix()
		_, err = tx.Exec("UPDATE users SET last_ts=? WHERE telegram_id=?",
			now, workerID)
		if err != nil {
			tx.Rollback()
			return "Err buf operation", false, err
		}
		tx.Commit()
		cb, _ := GetBalance(db, workerID)
		return fmt.Sprintf("Balance is topped up on: %d, current balance: %d", amount, cb), ok, nil
	}
}
