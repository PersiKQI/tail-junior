// Пакет bankfiles встраивает восстановленный комплект содержания в бинарники и тесты.
package bankfiles

import _ "embed"

//go:embed learning-program-server-draft.json
var Program []byte

//go:embed content-adapter.json
var Adapter []byte

//go:embed rules.json
var Rules []byte

//go:embed lab-case-LB01.json
var LabCase []byte

//go:embed calculation-fixtures.json
var CalculationFixtures []byte

//go:embed challenge-money-early.json
var Challenge []byte

//go:embed legacy-d7.json
var LegacyD7 []byte

//go:embed notification-templates.json
var NotificationTemplates []byte
