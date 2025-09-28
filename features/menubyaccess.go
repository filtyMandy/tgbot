package features

import tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

// GenMainMenu генерирует основной инлайн-клавиатурный блок по роли пользователя
func GenMainMenu(accessLevel string, userID, superUser int64) tgbotapi.InlineKeyboardMarkup {
	var kbRows [][]tgbotapi.InlineKeyboardButton
	if accessLevel == "worker" {
		kbRows = append(kbRows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🌟 Баланс", "show_balance"),
			tgbotapi.NewInlineKeyboardButtonData("🏪 Магазин", "menu_market"),
			tgbotapi.NewInlineKeyboardButtonData("🛍 Заказы", "history_orders"),
		))
	}
	if accessLevel == "manager" || accessLevel == "admin" {
		kbRows = append(kbRows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💰 Начислить", "topup_"),
			tgbotapi.NewInlineKeyboardButtonData("📋 Список", "menu_list"),
		))
	}
	if accessLevel == "admin" {
		kbRows = append(kbRows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✏️Данные", "menu_admin_setbal"),
			tgbotapi.NewInlineKeyboardButtonData("🏦️ Магазин", "shop_edit"),
			tgbotapi.NewInlineKeyboardButtonData("❗️Доступ", "accesslevel"),
			tgbotapi.NewInlineKeyboardButtonData("❇️Заказы", "orders"),
		))
	}
	if userID == superUser {
		kbRows = append(kbRows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Переход", "super_user:transition"),
			tgbotapi.NewInlineKeyboardButtonData("Доступ", "super_user:access"),
		))
	}

	if len(kbRows) == 0 {
		kbRows = [][]tgbotapi.InlineKeyboardButton{}
	}
	return tgbotapi.NewInlineKeyboardMarkup(kbRows...)
}
