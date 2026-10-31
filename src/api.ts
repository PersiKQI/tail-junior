import { Platform } from 'react-native';
import Constants from 'expo-constants';
import * as SecureStore from 'expo-secure-store';
import AsyncStorage from '@react-native-async-storage/async-storage';
import type { AppState, AnswerResult, GoalInput, Profile, BudgetInput, ActionInput, SimulationDecision, GoalSaveInput, ExpenseInput, InvestmentAnswerResult, InvestmentLabInput, InvestmentLabResult } from '../shared/types';

const TOKEN_KEY = 'teyl.session.v1';
const devHost = Constants.expoConfig?.hostUri?.split(':')[0];
export const API_URL = process.env.EXPO_PUBLIC_API_URL || (Platform.OS === 'web'
  ? `${globalThis.location?.protocol || 'http:'}//${globalThis.location?.hostname || 'localhost'}:3001`
  : `http://${devHost || 'localhost'}:3001`);
let token: string | null = null;
export class ApiError extends Error { code: string; constructor(message: string, code = 'NETWORK') { super(message); this.code = code; } }
async function saveToken(value: string | null) {
  token = value;
  if (Platform.OS === 'web') {
    if (value) await AsyncStorage.setItem(TOKEN_KEY, value); else await AsyncStorage.removeItem(TOKEN_KEY);
  } else {
    if (value) await SecureStore.setItemAsync(TOKEN_KEY, value); else await SecureStore.deleteItemAsync(TOKEN_KEY);
  }
}
async function request<T>(path: string, method = 'GET', body?: unknown, idempotencyKey?: string): Promise<T> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 12000);
  try {
    const response = await fetch(`${API_URL}/api${path}`, {
      method, signal: controller.signal,
      headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(idempotencyKey ? {'Idempotency-Key':idempotencyKey} : {}) },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    if (response.status === 204) return undefined as T;
    const result = await response.json();
    if (!response.ok) throw new ApiError(result.error || 'Не удалось сохранить. Попробуй ещё раз.', result.code);
    return result as T;
  } catch (error) {
    if (error instanceof ApiError) throw error;
    throw new ApiError('Нет связи с ТЭЙЛ. Проверь подключение и попробуй ещё раз.');
  } finally { clearTimeout(timer); }
}
export const api = {
  async bootstrap(): Promise<AppState> {
    token = Platform.OS === 'web' ? await AsyncStorage.getItem(TOKEN_KEY) : await SecureStore.getItemAsync(TOKEN_KEY);
    if (token) return await request<AppState>('/bootstrap');
    const result = await request<{token:string; state:AppState}>('/session', 'POST', {});
    await saveToken(result.token);
    return result.state;
  },
  refresh: () => request<AppState>('/bootstrap'),
  resetSession: () => saveToken(null),
  profile: (value: Partial<Profile>) => request<AppState>('/profile', 'PATCH', value),
  goal: (value: GoalSaveInput) => request<AppState>('/goal', 'PUT', value),
  budget: (value:BudgetInput) => request<AppState>('/budget','PUT',value),
  expense: (value:ExpenseInput,key:string) => request<AppState>('/expenses','POST',value,key),
  voidExpense: (id:string,voided:boolean) => request<AppState>(`/expenses/${id}`,'PATCH',{voided}),
  action: (value:ActionInput,key:string) => request<AppState>('/actions','POST',value,key),
  completeAction: (id:string) => request<AppState>(`/actions/${id}`,'PATCH',{completed:true}),
  removeAction: (id:string) => request<AppState>(`/actions/${id}`,'DELETE'),
  decision: (id:string,value:SimulationDecision) => request<AnswerResult>(`/simulations/${id}/decision`,'POST',value),
  answer: (id:string, choiceId:string) => request<AnswerResult>(`/lessons/${id}/answer`, 'POST', {choiceId}),
  simulate: (id:string, choiceId:string, reasonId:string) => request<AnswerResult>(`/simulations/${id}/answer`, 'POST', {choiceId,reasonId}),
  assess: (choiceId:string, reasonId:string) => request<AnswerResult>('/assessment/answer', 'POST', {choiceId,reasonId}),
  investmentAnswer: (id:string,choiceId:string,reasonId:string) => request<InvestmentAnswerResult>(`/investments/lessons/${id}/answer`,'POST',{choiceId,reasonId}),
  investmentLab: (value:InvestmentLabInput) => request<InvestmentLabResult>('/investments/lab','POST',value),
  checkin: (value:{type:'saved'|'reviewed';amount?:number;note?:string}, key:string) => request<AppState>('/checkins', 'POST', value, key),
  async remove() {
    try { await request<void>('/account', 'DELETE'); }
    catch (e) { if (!(e instanceof ApiError) || !['UNAUTHORIZED','SESSION_EXPIRED'].includes(e.code)) throw e; }
    await saveToken(null);
  },
};
