package database // Предполагается, что эти функции находятся в пакете database

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time" // Добавлено, если нужно будет для транзакций

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5" // Для SendWorkersList
)

// ChangeAccess обновляет уровень доступа пользователя.
func ChangeAccess(db *sql.DB, userID int64, accessLevel string) error {
	// В PostgreSQL плейсхолдеры нумеруются: $1, $2, ...
	query := `UPDATE users SET access_level = $1 WHERE telegram_id = $2`
	result, err := db.Exec(query, accessLevel, userID)
	if err != nil {
		log.Printf("Ошибка при обновлении access_level для %d: %v", userID, err)
		return fmt.Errorf("при обновлении access_level для %d: %w", userID, err) // Оборачиваем ошибку
	}

	rowsAffected, err := result.RowsAffected() // RowsAffected доступен для PostgreSQL
	if err != nil {
		log.Printf("Ошибка получения RowsAffected для %d: %v", userID, err)
		// Можно вернуть эту ошибку, но часто она не критична
	}

	if rowsAffected == 0 {
		log.Printf("Пользователь с telegram_id=%d не найден", userID)
		// Возможно, стоит вернуть ошибку, если пользователь не найден
		// return fmt.Errorf("пользователь с telegram_id=%d не найден", userID)
	} else {
		log.Printf("Уровень доступа для %d изменён на %s", userID, accessLevel)
	}

	return nil
}

// UpdateRest обновляет rest_number пользователя или создает нового, если не существует.
func UpdateRest(db *sql.DB, userID int64, value string) error {
	// Проверяем существование пользователя
	// PostgreSQL: EXISTS(SELECT 1 FROM ...) работает аналогично
	var exists bool
	// Используем $1 для плейсхолдера
	row := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE telegram_id = $1)`, userID)
	err := row.Scan(&exists)
	if err != nil {
		log.Printf("Ошибка при проверке существования пользователя %d: %v", userID, err)
		return fmt.Errorf("при проверке существования пользователя %d: %w", userID, err)
	}

	if exists {
		// Обновление существующего пользователя
		// Используем $1, $2 для плейсхолдеров
		_, err = db.Exec(`UPDATE users SET rest_number = $1 WHERE telegram_id = $2`, value, userID)
		if err != nil {
			log.Printf("Ошибка при обновлении пользователя %d: %v", userID, err)
			return fmt.Errorf("при обновлении пользователя %d: %w", userID, err)
		}
		log.Printf("rest_number для %d обновлен на %s", userID, value)
	} else {
		// Создание нового пользователя
		// INSERT INTO ... VALUES ($1, $2, ...)
		// Для 'SuperUser', '999', 'admin', 1 - это значения по умолчанию.
		// Убедись, что типы данных в твоей таблице соответствуют.
		// 'verified' часто boolean, но в твоем коде 1 (int) - ок, если колонка numeric/int.
		_, err = db.Exec(`
            INSERT INTO users(telegram_id, rest_number, name, table_number, access_level, verified)
            VALUES($1, $2, $3, $4, $5, $6)`,
			userID, value, "SuperUser", "999", "admin", 1) // Для PostgreSQL 'verified' лучше использовать TRUE/FALSE, если тип boolean
		if err != nil {
			log.Printf("Ошибка при создании пользователя %d: %v", userID, err)
			return fmt.Errorf("при создании пользователя %d: %w", userID, err)
		}
		log.Printf("Создан новый пользователь: telegram_id=%d, rest_number=%s", userID, value)
	}
	return nil
}

// DeleteUser удаляет пользователя по telegramID.
func DeleteUser(db *sql.DB, telegramID int64) error {
	// PostgreSQL: DELETE FROM ... WHERE ...
	_, err := db.Exec("DELETE FROM users WHERE telegram_id = $1", telegramID)
	if err != nil {
		log.Printf("Ошибка при удалении пользователя %d: %v", telegramID, err)
		return fmt.Errorf("при удалении пользователя %d: %w", telegramID, err)
	}
	return nil
}

// SendWorkersString формирует строку со списком сотрудников для определенного предприятия.
func SendWorkersString(db *sql.DB, fromID int64) (string, error) {
	// Предполагается, что SameRest() корректно возвращает rest_number для PostgreSQL
	restNum, err := SameRest(db, fromID)
	if err != nil {
		log.Printf("Ошибка получения rest_number для %d: %v", fromID, err)
		return "", fmt.Errorf("при получении rest_number для %d: %w", fromID, err)
	}
	// restNum должен быть целым числом для запроса. Если SameRest возвращает string,
	// нужно его преобразовать. Предполагаем, что он уже int.
	log.Printf("rest_number для %d: %d", fromID, restNum)

	// PostgreSQL: CAST(table_number AS INTEGER) ASC - стандартный синтаксис.
	// Плейсхолдеры $1, $2, ...
	query := `SELECT table_number, name, access_level, current_balance
              FROM users
              WHERE rest_number = $1
              ORDER BY CAST(table_number AS INTEGER) ASC`
	rows, err := db.Query(query, restNum)
	if err != nil {
		log.Printf("Ошибка загрузки списка сотрудников для rest_number %d: %v", restNum, err)
		return "", fmt.Errorf("при загрузке списка сотрудников для rest_number %d: %w", restNum, err)
	}
	defer rows.Close()

	var list strings.Builder
	for rows.Next() {
		var num, name, access string
		var balance int
		// Scan работает так же
		if err := rows.Scan(&num, &name, &access, &balance); err != nil {
			log.Printf("Ошибка сканирования строки в SendWorkersString (rest_number %d): %v", restNum, err)
			// Продолжаем, если одна строка некорректна, но лучше логировать
			continue
		}
		// Формат вывода остается прежним
		list.WriteString(fmt.Sprintf("%s %s|%s|%d🌟\n", num, name, access, balance))
	}

	if err = rows.Err(); err != nil {
		log.Printf("Ошибка итерации по строкам в SendWorkersString (rest_number %d): %v", restNum, err)
		return "", fmt.Errorf("при итерации по строкам для rest_number %d: %w", restNum, err)
	}
	return list.String(), nil
}

// ChangeRole меняет роль пользователя по номеру стола и проверяет, что они в одном предприятии.
func ChangeRole(db *sql.DB, oldAdminID int64, tableNumber, role string) error {
	// PostgreSQL: Используем $1, $2, $3 для подстановки.
	// subquery в WHERE работает так же.
	query := `
        SELECT telegram_id FROM users
        WHERE table_number = $1 AND rest_number = (
            SELECT rest_number FROM users WHERE telegram_id = $2
        ) LIMIT 1`
	var newUserID int64
	err := db.QueryRow(query, tableNumber, oldAdminID).Scan(&newUserID)

	if err == sql.ErrNoRows {
		return errors.New("пользователь с указанным номером стола и вашего предприятия не найден")
	}
	if err != nil {
		log.Printf("Ошибка поиска пользователя для смены роли (oldAdminID=%d, tableNumber=%s): %v", oldAdminID, tableNumber, err)
		return fmt.Errorf("при поиске пользователя для смены роли: %w", err)
	}

	// Применить роль
	// $1 - role, $2 - newUserID
	updateRoleQuery := `UPDATE users SET access_level = $1 WHERE telegram_id = $2`
	_, err = db.Exec(updateRoleQuery, role, newUserID)
	if err != nil {
		log.Printf("Ошибка при обновлении роли для %d на %s: %v", newUserID, role, err)
		return fmt.Errorf("при обновлении роли для %d: %w", newUserID, err)
	}

	// Если роль — admin, то понижаем старого админа
	if role == "admin" {
		// $1 - oldAdminID
		demoteQuery := `UPDATE users SET access_level = 'manager' WHERE telegram_id = $1`
		_, err = db.Exec(demoteQuery, oldAdminID)
		if err != nil {
			log.Printf("Ошибка при понижении старого админа %d: %v", oldAdminID, err)
			// Это критичная ошибка, т.к. новый админ назначен, но старый не понижен
			return fmt.Errorf("при понижении старого админа %d: %w", oldAdminID, err)
		}
		log.Printf("Старый админ %d понижен до manager", oldAdminID)
	}
	log.Printf("Роль пользователя %d изменена на %s", newUserID, role)
	return nil
}

// GetWorkerInfoValues получает полную информацию о сотруднике.
func GetWorkerInfoValues(db *sql.DB, workerID int64) (string, string, string, int, error) {
	var access, name, tableNumber string
	var balance int
	// PostgreSQL: SELECT ... WHERE telegram_id = $1
	query := `SELECT table_number, name, access_level, current_balance
              FROM users
              WHERE telegram_id = $1`
	err := db.QueryRow(query, workerID).Scan(&tableNumber, &name, &access, &balance)
	if err != nil {
		log.Printf("Ошибка получения информации о сотруднике %d: %v", workerID, err)
		return "", "", "", 0, fmt.Errorf("при получении информации о сотруднике %d: %w", workerID, err)
	}
	return tableNumber, name, access, balance, nil
}

// GetWorkerInfo форматирует информацию о сотруднике в строку.
// Эта функция использует GetWorkerInfoValues, поэтому ее можно переписать,
// если GetWorkerInfoValues будет изменена.
func GetWorkerInfo(db *sql.DB, workerID int64) string {
	tableNumber, name, access, balance, err := GetWorkerInfoValues(db, workerID)
	if err != nil {
		log.Printf("Не удалось получить информацию для форматирования: %v", err)
		return "Ошибка получения данных сотрудника."
	}
	return fmt.Sprintf("Вы выбрали %s %s\nУровень доступа: %s\nТекущий баланс: %d",
		tableNumber, name, access, balance)
}

// ApplyCorrection применяет изменения к данным пользователя.
func ApplyCorrection(db *sql.DB, workerID int64, field, value string) error {
	// DELETE - Специальный случай
	if field == "delete" {
		// Для большей безопасности можно проверить, что value == "true" или "1",
		// но если удаление происходит только по workerID, это может быть излишним.
		// PostgreSQL: DELETE FROM ... WHERE ...
		_, err := db.Exec("DELETE FROM users WHERE telegram_id = $1", workerID)
		if err != nil {
			log.Printf("Ошибка при удалении пользователя %d: %v", workerID, err)
			return fmt.Errorf("не удалось удалить пользователя %d: %w", workerID, err)
		}
		log.Printf("Пользователь %d успешно удален", workerID)
		return nil
	}

	var query string
	var n int // Для числовых полей

	// Валидация числовых полей перед построением запроса
	switch field {
	case "balance", "tablenumber":
		var errConv error
		n, errConv = strconv.Atoi(value)
		if errConv != nil {
			return fmt.Errorf("значение для поля '%s' должно быть целым числом", field)
		}
		if n < 0 {
			return fmt.Errorf("значение для поля '%s' не может быть отрицательным", field)
		}
	}

	// Построение SQL запроса в зависимости от поля
	switch field {
	case "balance":
		// PostgreSQL: UPDATE ... SET ... = $1 WHERE telegram_id = $2
		query = "UPDATE users SET current_balance = $1 WHERE telegram_id = $2"
		value = strconv.Itoa(n) // Преобразуем число обратно в строку для Exec, если нужно
	case "name":
		query = "UPDATE users SET name = $1 WHERE telegram_id = $2"
	case "tablenumber":
		query = "UPDATE users SET table_number = $1 WHERE telegram_id = $2"
		value = strconv.Itoa(n) // Преобразуем число обратно в строку
	default:
		return fmt.Errorf("неизвестное поле для коррекции: %s", field)
	}

	_, err := db.Exec(query, value, workerID)
	if err != nil {
		log.Printf("Ошибка при применении коррекции поля '%s' для пользователя %d: %v", field, workerID, err)
		return fmt.Errorf("при применении коррекции поля '%s' для пользователя %d: %w", field, workerID, err)
	}
	log.Printf("Поле '%s' пользователя %d успешно изменено на '%s'", field, workerID, value)
	return nil
}

// GetAccessLevel получает уровень доступа пользователя.
func GetAccessLevel(db *sql.DB, userID int64) (string, error) {
	var accessLevel string
	// PostgreSQL: SELECT ... WHERE telegram_id = $1
	query := `SELECT access_level FROM users WHERE telegram_id = $1`
	err := db.QueryRow(query, userID).Scan(&accessLevel)
	if err != nil {
		// Если пользователя нет, Scan вернет sql.ErrNoRows
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("пользователь с telegram_id=%d не найден", userID)
		}
		log.Printf("Ошибка получения access_level для %d: %v", userID, err)
		return "", fmt.Errorf("при получении access_level для %d: %w", userID, err)
	}
	return accessLevel, nil
}

// GetUserDep получает rest_number (предприятие) пользователя.
// Предполагается, что SameRest() вызывается отдельно, если нужен rest_number текущего пользователя.
func GetUserDep(db *sql.DB, telegramID int64) (string, error) {
	var dep string
	// PostgreSQL: SELECT ... WHERE telegram_id = $1
	query := `SELECT rest_number FROM users WHERE telegram_id = $1`
	err := db.QueryRow(query, telegramID).Scan(&dep)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("пользователь с telegram_id=%d не найден", telegramID)
		}
		log.Printf("Ошибка получения rest_number для %d: %v", telegramID, err)
		return "", fmt.Errorf("при получении rest_number для %d: %w", telegramID, err)
	}
	return dep, nil
}

// SendWorkersList отправляет список работников с пагинацией.
func SendWorkersList(bot *tgbotapi.BotAPI, db *sql.DB, chatID int64, status string, dep string, page int) error {
	const pageSize = 15

	// Считаем общее количество работников.
	// PostgreSQL: COUNT(*)
	// Проверяем, что dep - это строка, а не число, если rest_number в базе строка.
	var total int
	countQuery := `SELECT COUNT(*) FROM users WHERE rest_number = $1 AND access_level = 'worker' AND verified = 1` // verified=TRUE для boolean
	err := db.QueryRow(countQuery, dep).Scan(&total)
	if err != nil {
		log.Printf("Ошибка получения количества работников для dep %s: %v", dep, err)
		bot.Send(tgbotapi.NewMessage(chatID, "Ошибка получения количества работников."))
		return fmt.Errorf("при получении количества работников для dep %s: %w", dep, err)
	}

	if total == 0 {
		bot.Send(tgbotapi.NewMessage(chatID, "❌ В вашем предприятии нет работников."))
		return nil
	}

	offset := page * pageSize

	// Получаем работников с пагинацией.
	// PostgreSQL: LIMIT ?, OFFSET ? - здесь тоже $1, $2, $3
	// Убедись, что 'dep' - это строка, если rest_number в базе строка.
	query := `SELECT telegram_id, name, table_number
              FROM users
              WHERE rest_number = $1 AND access_level = 'worker' AND verified = 1
              ORDER BY CAST(table_number AS INTEGER) ASC
              LIMIT $2 OFFSET $3`
	rows, err := db.Query(query, dep, pageSize, offset)
	if err != nil {
		log.Printf("Ошибка получения работников для dep %s (page %d): %v", dep, page, err)
		bot.Send(tgbotapi.NewMessage(chatID, "Ошибка получения работников."))
		return fmt.Errorf("при получении работников для dep %s (page %d): %w", dep, page, err)
	}
	defer rows.Close()

	buttons := [][]tgbotapi.InlineKeyboardButton{}
	rowCount := 0
	for rows.Next() {
		var workerID int64
		var name, tableNum string
		if err := rows.Scan(&workerID, &name, &tableNum); err != nil {
			log.Printf("Ошибка сканирования строки работника (dep %s): %v", dep, err)
			continue // Пропускаем некорректную строку
		}
		btnText := fmt.Sprintf("%s %s", tableNum, name)
		callbackData := fmt.Sprintf("%s:%d", status, workerID)
		buttons = append(buttons, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(btnText, callbackData),
		))
		rowCount++
	}

	// Если после запроса строк нет, но total > 0 (например, из-за предыдущей ошибки),
	// или просто строк нет для текущей страницы
	if rowCount == 0 && total > 0 {
		// Это может случиться, если offset >= total, но total был подсчитан ранее.
		// Или если все строки были некорректны.
		bot.Send(tgbotapi.NewMessage(chatID, "На этой странице нет работников. Попробуйте перейти назад."))
		return nil
	} else if rowCount == 0 && total == 0 {
		// Этот случай уже обработан выше (total == 0)
		return nil
	}

	// Кнопки пагинации:
	paginationButtons := []tgbotapi.InlineKeyboardButton{}
	if page > 0 {
		// Формат callbackData для пагинации
		prevCallback := fmt.Sprintf("topup_select_worker:%d:%s:%s", page-1, status, dep)
		paginationButtons = append(paginationButtons,
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад", prevCallback),
		)
	}
	// offset+pageSize < total означает, что есть еще элементы после текущей страницы
	if offset+pageSize < total {
		nextCallback := fmt.Sprintf("topup_select_worker:%d:%s:%s", page+1, status, dep)
		paginationButtons = append(paginationButtons,
			tgbotapi.NewInlineKeyboardButtonData("➡️ Дальше", nextCallback),
		)
	}
	if len(paginationButtons) > 0 {
		buttons = append(buttons, paginationButtons)
	}

	replyMsg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Выберите работника (страница %d/%d):", page+1, (total+pageSize-1)/pageSize)) // Расчет общего числа страниц
	replyMsg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(buttons...)
	_, err = bot.Send(replyMsg)
	if err != nil {
		log.Printf("Ошибка отправки сообщения с работниками для chatID %d: %v", chatID, err)
		return fmt.Errorf("при отправке сообщения с работниками: %w", err)
	}
	return nil
}

func GetAdminID(db *sql.DB, userID int64) (int64, error) {
	var adminTelegramID int64
	// Получаем rest_number пользователя
	rn, err := SameRest(db, userID) // Предполагается, что SameRest возвращает int64
	if err != nil {
		log.Printf("Ошибка получения rest_number для пользователя %d: %v", userID, err)
		return 0, fmt.Errorf("при получении rest_number для пользователя %d: %w", userID, err)
	}
	query := `SELECT telegram_id FROM users WHERE rest_number = $1 AND access_level = 'admin' LIMIT 1`
	err = db.QueryRow(query, rn).Scan(&adminTelegramID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Printf("Нет админа для предприятия (rest_number: %d)", rn)
			return 0, fmt.Errorf("администратор для предприятия %d не найден", rn)
		}
		log.Printf("Ошибка при поиске админа для предприятия %d: %v", rn, err)
		return 0, fmt.Errorf("при поиске админа для предприятия %d: %w", rn, err)
	}
	return adminTelegramID, nil
}

func GetBalance(db *sql.DB, userID int64) (int, error) {
	var balance int
	// PostgreSQL: SELECT current_balance FROM users WHERE telegram_id = $1
	query := `SELECT current_balance FROM users WHERE telegram_id = $1`
	err := db.QueryRow(query, userID).Scan(&balance)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Printf("Пользователь с telegram_id=%d не найден при получении баланса", userID)
			return 0, fmt.Errorf("пользователь с telegram_id=%d не найден", userID)
		}
		log.Printf("Ошибка при получении баланса для %d: %v", userID, err)
		return 0, fmt.Errorf("при получении баланса для %d: %w", userID, err)
	}
	return balance, nil
}

func SameRest(db *sql.DB, userID int64) (int64, error) {
	var rn int64
	// PostgreSQL: SELECT rest_number FROM users WHERE telegram_id = $1
	query := `SELECT rest_number FROM users WHERE telegram_id = $1`
	err := db.QueryRow(query, userID).Scan(&rn)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Printf("Пользователь с telegram_id=%d не найден при получении rest_number", userID)
			return 0, fmt.Errorf("пользователь с telegram_id=%d не найден", userID)
		}
		log.Printf("Ошибка при получении rest_number для %d: %v", userID, err)
		return 0, fmt.Errorf("при получении rest_number для %d: %w", userID, err)
	}
	return rn, nil
}

// CanManagerChangeBalance проверяет, прошло ли достаточно времени с последнего изменения баланса менеджером.
func CanManagerChangeBalance(db *sql.DB, workerID int64) (bool, string) {
	var lastTs int64
	// PostgreSQL: SELECT last_ts FROM users WHERE telegram_id = $1
	query := `SELECT last_ts FROM users WHERE telegram_id = $1`
	// Обрабатываем sql.ErrNoRows, если пользователя нет (хотя в контексте TopUpBalance, он скорее всего есть)
	err := db.QueryRow(query, workerID).Scan(&lastTs)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Printf("Пользователь %d не найден для проверки last_ts", workerID)
			return false, "Пользователь не найден"
		}
		log.Printf("Ошибка при чтении last_ts для %d: %v", workerID, err)
		return false, "Ошибка базы данных при проверке времени"
	}

	// Проверяем, если last_ts == 0 (т.е. еще не было изменений)
	if lastTs == 0 {
		return true, "" // Нет кулдауна, можно менять
	}

	// Вычисляем разницу в секундах
	currentTime := time.Now().Unix()
	elapsedSeconds := currentTime - lastTs
	cooldownSeconds := 12 * 3600 // 12 часов в секундах

	if elapsedSeconds < int64(cooldownSeconds) {
		leftSeconds := int64(cooldownSeconds) - elapsedSeconds
		hours := leftSeconds / 3600
		mins := (leftSeconds % 3600) / 60
		// Осторожно с форматированием "housrs" - это опечатка в оригинале. Исправлено на "hours".
		return false, fmt.Sprintf("❗ Кудаун: %d часов %d минут", hours, mins)
	}

	return true, "" // Кудаун прошел
}

// TopUpBalance пополняет баланс пользователя, учитывая кулдаун менеджера.
func TopUpBalance(db *sql.DB, workerID int64, amount int) (string, bool, error) {
	// Проверяем кулдаун менеджера
	ok, msg := CanManagerChangeBalance(db, workerID)
	if !ok {
		// Кулдаун активен
		return msg, false, nil
	}

	// Если кулдаун прошел, начинаем транзакцию
	tx, err := db.Begin()
	if err != nil {
		log.Printf("Ошибка начала транзакции для пополнения баланса %d: %v", workerID, err)
		return "Ошибка начала транзакции", false, fmt.Errorf("при начале транзакции: %w", err)
	}
	// Важно: если произойдет ошибка, транзакция будет отменена
	defer func() {
		if r := recover(); r != nil { // Обработка паник
			tx.Rollback()
			log.Printf("Panic при пополнении баланса %d: %v", workerID, r)
		} else if err != nil { // Если была другая ошибка
			tx.Rollback()
			log.Printf("Ошибка при выполнении транзакции для %d: %v", workerID, err)
		}
	}()

	// Повышение баланса
	// PostgreSQL: UPDATE users SET current_balance = current_balance + $1 WHERE telegram_id = $2
	updateBalanceQuery := `UPDATE users SET current_balance = current_balance + $1 WHERE telegram_id = $2`
	_, err = tx.Exec(updateBalanceQuery, amount, workerID)
	if err != nil {
		// Ошибка уже будет логирована в defer, просто возвращаем сообщение
		return "Ошибка обновления баланса", false, fmt.Errorf("при обновлении баланса: %w", err)
	}

	// Обновляем last_ts на текущее время
	now := time.Now().Unix()
	// PostgreSQL: UPDATE users SET last_ts = $1 WHERE telegram_id = $2
	updateTsQuery := `UPDATE users SET last_ts = $1 WHERE telegram_id = $2`
	_, err = tx.Exec(updateTsQuery, now, workerID)
	if err != nil {
		// Ошибка уже будет логирована в defer
		return "Ошибка обновления времени операции", false, fmt.Errorf("при обновлении last_ts: %w", err)
	}

	// Завершаем транзакцию
	err = tx.Commit()
	if err != nil {
		// Ошибка уже будет логирована в defer
		return "Ошибка коммита транзакции", false, fmt.Errorf("при коммите транзакции: %w", err)
	}

	// Получаем новый баланс для сообщения
	// Важно: GetBalance использует отдельное соединение или просто db.QueryRow.
	// Если хочешь получить баланс из той же транзакции, нужно использовать tx.QueryRow.
	// Для простоты, используем db, но учти, что это может быть не абсолютно точное значение,
	// если были другие параллельные операции.
	cb, getBalanceErr := GetBalance(db, workerID) // Получаем обновленный баланс
	if getBalanceErr != nil {
		log.Printf("Ошибка получения нового баланса для %d после пополнения: %v", workerID, getBalanceErr)
		// Можно вернуть сообщение и текущий баланс, или ошибку
		return fmt.Sprintf("Баланс пополнен на: %d. Ошибка получения нового баланса.", amount), ok, nil
	}

	message := fmt.Sprintf("Баланс успешно пополнен на %d. Текущий баланс: %d", amount, cb)
	return message, ok, nil
}
