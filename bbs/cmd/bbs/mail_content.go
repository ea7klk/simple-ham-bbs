package main

import "fmt"

// Mail bodies.
//
// These are not UI strings, so they live here rather than in translations.json:
// each message is several paragraphs plus a URL that must not be wrapped, and
// the four languages are kept side by side where a translator can compare them.
// The user's chosen language selects the template; anything unrecognised falls
// back to English.

type mailStrings struct {
	subjectActivation string
	activation        string
	subjectReset      string
	reset             string
	subjectEmailOld   string
	emailOld          string
	subjectEmailNew   string
	emailNew          string
	why               string
	ignore            string
}

var mailTexts = map[string]mailStrings{
	"en": {
		subjectActivation: "Your %[1]s activation code",
		activation: "Hello %[1]s,\n\n" +
			"your account on %[2]s has been created. Enter this code to activate it:\n\n" +
			"    %[3]s\n\n" +
			"The code expires in 24 hours and can be used once.\n\n" +
			"If you did not register, no action is needed: the account stays inactive and unused.",
		subjectReset: "Reset your %[1]s password",
		reset: "Hello %[1]s,\n\n" +
			"someone asked to reset the password for your account on %[2]s.\n\n" +
			"Open this link to choose a new password:\n\n%[3]s\n\n" +
			"The link expires in 2 hours and works once.\n\n" +
			"If this was not you, ignore this message: your password has not changed.",
		subjectEmailOld: "Confirm the email change on %[1]s",
		emailOld: "Hello %[1]s,\n\n" +
			"a request was made to change the email address on your %[2]s account to:\n\n" +
			"    %[4]s\n\n" +
			"Approve the change from this address:\n\n%[3]s\n\n" +
			"The link expires in 24 hours. The change takes effect only after the new\n" +
			"address is confirmed as well.\n\n" +
			"If you did not request this, ignore this message and the change will not happen.",
		subjectEmailNew: "Validate your new address on %[1]s",
		emailNew: "Hello %[1]s,\n\n" +
			"this address was given as the new email for the %[2]s account.\n\n" +
			"Validate it here:\n\n%[3]s\n\n" +
			"The link expires in 24 hours. The change takes effect only after the\n" +
			"current address confirms it as well.\n\n" +
			"If you did not request this, ignore this message and nothing will change.",
	},
	"de": {
		subjectActivation: "Dein %[1]s-Aktivierungscode",
		activation: "Hallo %[1]s,\n\n" +
			"dein Konto auf %[2]s wurde angelegt. Gib diesen Code ein, um es zu aktivieren:\n\n" +
			"    %[3]s\n\n" +
			"Der Code ist 24 Stunden gültig und kann einmal verwendet werden.\n\n" +
			"Wenn du dich nicht registriert hast, ist nichts zu tun: das Konto bleibt\n" +
			"inaktiv und unbenutzt.",
		subjectReset: "Passwort für %[1]s zurücksetzen",
		reset: "Hallo %[1]s,\n\n" +
			"jemand hat darum gebeten, das Passwort für dein Konto auf %[2]s zurückzusetzen.\n\n" +
			"Öffne diesen Link, um ein neues Passwort zu wählen:\n\n%[3]s\n\n" +
			"Der Link ist 2 Stunden gültig und funktioniert einmal.\n\n" +
			"Wenn du das nicht warst, ignoriere diese Nachricht: dein Passwort ist\n" +
			"unverändert.",
		subjectEmailOld: "E-Mail-Änderung auf %[1]s bestätigen",
		emailOld: "Hallo %[1]s,\n\n" +
			"es wurde beantragt, die E-Mail-Adresse deines Kontos auf %[2]s zu ändern auf:\n\n" +
			"    %[4]s\n\n" +
			"Bestätige die Änderung von dieser Adresse aus:\n\n%[3]s\n\n" +
			"Der Link ist 24 Stunden gültig. Die Änderung wird erst wirksam, wenn auch die\n" +
			"neue Adresse bestätigt ist.\n\n" +
			"Wenn du das nicht beantragt hast, ignoriere diese Nachricht und die Änderung\n" +
			"findet nicht statt.",
		subjectEmailNew: "Neue Adresse auf %[1]s bestätigen",
		emailNew: "Hallo %[1]s,\n\n" +
			"diese Adresse wurde als neue E-Mail für das Konto auf %[2]s angegeben.\n\n" +
			"Bestätige sie hier:\n\n%[3]s\n\n" +
			"Der Link ist 24 Stunden gültig. Die Änderung wird erst wirksam, wenn auch die\n" +
			"aktuelle Adresse zustimmt.\n\n" +
			"Wenn du das nicht beantragt hast, ignoriere diese Nachricht und es ändert\n" +
			"sich nichts.",
	},
	"es": {
		subjectActivation: "Tu código de activación de %[1]s",
		activation: "Hola %[1]s:\n\n" +
			"se ha creado tu cuenta en %[2]s. Introduce este código para activarla:\n\n" +
			"    %[3]s\n\n" +
			"El código caduca en 24 horas y solo se puede usar una vez.\n\n" +
			"Si no te has registrado, no hace falta hacer nada: la cuenta queda\n" +
			"inactiva y sin usar.",
		subjectReset: "Restablecer tu contraseña de %[1]s",
		reset: "Hola %[1]s:\n\n" +
			"alguien ha pedido restablecer la contraseña de tu cuenta en %[2]s.\n\n" +
			"Abre este enlace para elegir una nueva contraseña:\n\n%[3]s\n\n" +
			"El enlace caduca en 2 horas y funciona una sola vez.\n\n" +
			"Si no has sido tú, ignora este mensaje: tu contraseña no ha cambiado.",
		subjectEmailOld: "Confirma el cambio de correo en %[1]s",
		emailOld: "Hola %[1]s:\n\n" +
			"se ha solicitado cambiar la dirección de correo de tu cuenta en %[2]s a:\n\n" +
			"    %[4]s\n\n" +
			"Aprueba el cambio desde esta dirección:\n\n%[3]s\n\n" +
			"El enlace caduca en 24 horas. El cambio solo se aplica cuando también se\n" +
			"confirme la nueva dirección.\n\n" +
			"Si no lo has solicitado, ignora este mensaje y el cambio no se hará.",
		subjectEmailNew: "Valida tu nueva dirección en %[1]s",
		emailNew: "Hola %[1]s:\n\n" +
			"esta dirección se ha indicado como nuevo correo para la cuenta en %[2]s.\n\n" +
			"Valídala aquí:\n\n%[3]s\n\n" +
			"El enlace caduca en 24 horas. El cambio solo se aplica cuando también lo\n" +
			"confirme la dirección actual.\n\n" +
			"Si no lo has solicitado, ignora este mensaje y nada cambiará.",
	},
	"fr": {
		subjectActivation: "Votre code d'activation %[1]s",
		activation: "Bonjour %[1]s,\n\n" +
			"votre compte sur %[2]s a été créé. Saisissez ce code pour l'activer :\n\n" +
			"    %[3]s\n\n" +
			"Le code expire dans 24 heures et ne fonctionne qu'une fois.\n\n" +
			"Si vous ne vous êtes pas inscrit, rien à faire : le compte reste inactif\n" +
			"et inutilisé.",
		subjectReset: "Réinitialiser votre mot de passe %[1]s",
		reset: "Bonjour %[1]s,\n\n" +
			"quelqu'un a demandé à réinitialiser le mot de passe de votre compte sur %[2]s.\n\n" +
			"Ouvrez ce lien pour choisir un nouveau mot de passe :\n\n%[3]s\n\n" +
			"Le lien expire dans 2 heures et ne fonctionne qu'une fois.\n\n" +
			"Si ce n'est pas vous, ignorez ce message : votre mot de passe est inchangé.",
		subjectEmailOld: "Confirmez le changement d'e-mail sur %[1]s",
		emailOld: "Bonjour %[1]s,\n\n" +
			"une demande de changement de l'adresse e-mail de votre compte sur %[2]s a\n" +
			"été faite vers :\n\n" +
			"    %[4]s\n\n" +
			"Approuvez le changement depuis cette adresse :\n\n%[3]s\n\n" +
			"Le lien expire dans 24 heures. Le changement ne s'applique qu'une fois la\n" +
			"nouvelle adresse confirmée également.\n\n" +
			"Si vous n'êtes pas à l'origine de cette demande, ignorez ce message et le\n" +
			"changement n'aura pas lieu.",
		subjectEmailNew: "Validez votre nouvelle adresse sur %[1]s",
		emailNew: "Bonjour %[1]s,\n\n" +
			"cette adresse a été indiquée comme nouvelle adresse e-mail pour le compte\n" +
			"sur %[2]s.\n\n" +
			"Validez-la ici :\n\n%[3]s\n\n" +
			"Le lien expire dans 24 heures. Le changement ne s'applique qu'une fois\n" +
			"l'adresse actuelle confirmée également.\n\n" +
			"Si vous n'êtes pas à l'origine de cette demande, ignorez ce message et rien\n" +
			"ne changera.",
	},
}

func mailText(lang string) mailStrings {
	if texts, ok := mailTexts[lang]; ok {
		return texts
	}
	return mailTexts["en"]
}

// activationMail builds the registration message.
func activationMail(lang, callsign, code, siteName string) mailMessage {
	t := mailText(lang)
	return mailMessage{
		To:      "",
		Subject: fmt.Sprintf(t.subjectActivation, siteName),
		Body:    wrapBody(fmt.Sprintf(t.activation, callsign, siteName, code), mailBodyWidth) + "\n",
	}
}

func passwordResetMail(lang, callsign, link, siteName string) mailMessage {
	t := mailText(lang)
	return mailMessage{
		Subject: fmt.Sprintf(t.subjectReset, siteName),
		Body:    wrapBody(fmt.Sprintf(t.reset, callsign, siteName, link), mailBodyWidth) + "\n",
	}
}

func emailChangeMail(lang, callsign, link, siteName, newEmail string, toOldAddress bool) mailMessage {
	t := mailText(lang)
	if toOldAddress {
		return mailMessage{
			Subject: fmt.Sprintf(t.subjectEmailOld, siteName),
			Body:    wrapBody(fmt.Sprintf(t.emailOld, callsign, siteName, link, newEmail), mailBodyWidth) + "\n",
		}
	}
	return mailMessage{
		Subject: fmt.Sprintf(t.subjectEmailNew, siteName),
		Body:    wrapBody(fmt.Sprintf(t.emailNew, callsign, siteName, link), mailBodyWidth) + "\n",
	}
}
