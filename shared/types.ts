export type AgeBand = '14-17' | '18-20' | '21-22' | 'other';
export type CharacterId = 'lumi' | 'spark' | 'nova';
export interface Profile { name: string; ageBand: AgeBand; onboardingCompleted: boolean; reducedMotion: boolean; character: CharacterId | null; focus: 'curious' | 'goal' | 'gentle' | null; focusUpdatedAt: string | null; createdAt: string }
export interface GoalInput { title: string; target: number; saved: number; months: number; monthly: number; income: number; essential: number; reserve: number }
export interface GoalSaveInput extends GoalInput { categories?: { id: BudgetCategoryId; amount: number; title?: string }[] }
export interface Goal extends GoalInput { id: string; available: number; requiredMonthly: number; projected: number; feasible: boolean; suggestedMonths: number | null; updatedAt: string }
export interface Choice { id: string; text: string }
export interface Lesson { id: string; order: number; title: string; subtitle: string; category: string; duration: string; question: string; context: string; choices: Choice[]; completed: boolean; unlocked: boolean }
export interface Simulation { id: string; title: string; subtitle: string; context: string; question: string; facts: {label:string;value:string}[]; choices: Choice[]; reasons: Choice[]; completed: boolean; unlocked: boolean }
export interface Progress { completedLessons: number; completedSimulations: number; totalLessons: number; totalSimulations: number; firstAnswerAt: string | null; nextLessonId: string | null; nextSimulationId: string | null }
export interface Checkin { id: string; type: 'saved' | 'reviewed'; amount: number | null; note: string; createdAt: string }
export type BudgetCategoryId = 'food' | 'transport' | 'housing' | 'other';
export interface BudgetCategory { id: BudgetCategoryId; title: string; amount: number }
export interface BudgetInput { income: number; reserve: number; monthly: number; categories: { id: BudgetCategoryId; amount: number; title?: string }[] }
export interface Budget { income: number; reserve: number; monthly: number; categories: BudgetCategory[]; available: number; unallocated: number }
export interface ExpenseInput { amount: number; category: BudgetCategoryId; occurredOn: string; note?: string; timezoneOffsetMinutes?: number }
export interface Expense { id: string; amount: number; category: BudgetCategoryId; occurredOn: string; note: string; voidedAt: string | null; createdAt: string }
export interface PlannedAction { id: string; type: 'save' | 'review'; amount: number | null; note: string; dueDate: string; completedAt: string | null; createdAt: string }
export interface ActionInput { type: 'save' | 'review'; amount?: number; note: string; dueDate: string }
export interface SimulationDecision { monthly: number; months: number; reasonId: string }
export interface SimulationOutcome { monthly: number; months: number; available: number; projected: number | null; gap: number | null; feasible: boolean; remaining: number }
export interface Assessment { status: 'not_started' | 'waiting' | 'available' | 'expired' | 'completed'; availableAt: string | null; closesAt: string | null; correct: boolean | null; question?: string; context?: string; choices?: Choice[]; reasons?: Choice[] }
export type InvestmentAssetId = 'cash' | 'bond' | 'stock' | 'fund';
export type InvestmentScenarioId = 'growth' | 'down' | 'early';
export type InvestmentAllocation = Record<InvestmentAssetId,number>;
export interface InvestmentPortfolioRow { asset: InvestmentAssetId; units: number; initial: number; ending: number; cashIncome: number; priceChange: number; fees: number; profit: number }
export interface InvestmentPortfolioResult { scenarioId: InvestmentScenarioId; initial: number; ending: number; cashIncome: number; priceChange: number; fees: number; profit: number; rows: InvestmentPortfolioRow[] }
export interface InvestmentAttempt { choiceId: string; reasonId: string; correct: boolean; createdAt: string }
export interface InvestmentLesson { id: string; order: number; title: string; subtitle: string; duration: string; concept: string; example: string; question: string; choices: Choice[]; reasons: Choice[]; sources: {title:string;url:string}[]; completed: boolean; unlocked: boolean; firstAttempt: InvestmentAttempt|null; latestAttempt: InvestmentAttempt|null }
export interface InvestmentLabInput { allocation: InvestmentAllocation; scenarioId: InvestmentScenarioId; reasonId: string }
export interface InvestmentLabState { allocation: InvestmentAllocation; lastScenarioId: InvestmentScenarioId|null; lastResult: InvestmentPortfolioResult|null; lastReasonId: string|null; lastCorrect: boolean|null; reviewedScenarioIds: InvestmentScenarioId[]; updatedAt: string|null }
export interface InvestmentsState { lessons: InvestmentLesson[]; progress: {completedLessons:number;totalLessons:number;nextLessonId:string|null}; lab: InvestmentLabState; contentVersion: string }
export interface InvestmentAnswerResult { correct: boolean; title: string; explanation: string; alreadyCompleted: boolean; state: AppState }
export interface InvestmentLabResult { correct: boolean; title: string; explanation: string; result: InvestmentPortfolioResult; state: AppState }
export interface AppState { profile: Profile; goal: Goal | null; budget: Budget; expenses: Expense[]; actions: PlannedAction[]; lessons: Lesson[]; simulations: Simulation[]; progress: Progress; checkins: Checkin[]; assessment: Assessment; investments: InvestmentsState; contentVersion: string }
export interface AnswerResult { correct: boolean; title: string; explanation: string; alreadyCompleted: boolean; outcome?: SimulationOutcome; state: AppState }
export interface ApiErrorBody { error: string; code: string }
