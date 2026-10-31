import React, { useRef, useState } from 'react';
import { Keyboard, Pressable, ScrollView, View } from 'react-native';
import type { AppState, GoalInput } from '../shared/types';
import { api } from './api';
import { Button, Card, colors, ErrorNote, Field, formatMoney, Icon, IconButton, s, Txt } from './ui';

export { GoalScreen } from './GoalOverview';

type Form = Record<keyof GoalInput,string>;
type FormErrors = Partial<Record<keyof Form,string>>;
const amountKeys=['target','saved','monthly','income','essential','reserve'] as const;
const optionalKeys=['saved','income','essential','reserve'] as const;
const isWhole=(value:string,min=0,max=1000000)=>/^\d+$/.test(value)&&Number.isSafeInteger(Number(value))&&Number(value)>=min&&Number(value)<=max;
const number=(value:string)=>Number.isFinite(Number(value))?Number(value):0;

function initialForm(state:AppState):Form {
  if(state.goal)return Object.fromEntries((['title','target','saved','months','monthly','income','essential','reserve'] as const).map(key=>[key,String(state.goal![key])])) as Form;
  return {
    title:'',target:'',saved:'0',months:'',monthly:state.budget.monthly>0?String(state.budget.monthly):'',
    income:String(state.budget.income),essential:String(state.budget.categories.reduce((sum,category)=>sum+category.amount,0)),reserve:String(state.budget.reserve),
  };
}

function FormField({error,...props}:React.ComponentProps<typeof Field>&{error?:string}) {
  return <View style={{gap:7}}>
    <Field {...props}/>
    {error&&<Txt accessibilityRole="alert" size={12} color={colors.red}>{error}</Txt>}
  </View>;
}

export function GoalEditor({state,onSave,onClose,onBusyChange}:{onBusyChange:(v:boolean)=>void;state:AppState;onSave:(s:AppState)=>void;onClose:()=>void}) {
  const [form,setForm]=useState<Form>(()=>initialForm(state));
  const [advanced,setAdvanced]=useState(false);
  const [busy,setBusy]=useState(false);
  const [errors,setErrors]=useState<FormErrors>({});
  const [error,setError]=useState<string|null>(null);
  const monthlyEdited=useRef(Boolean(state.goal)||state.budget.monthly>0);
  const sending=useRef(false);
  const scroll=useRef<React.ComponentRef<typeof ScrollView>>(null);
  const n=(key:keyof Form)=>number(form[key]);
  const available=Math.max(0,n('income')-n('essential')-n('reserve'));
  const remaining=Math.max(0,n('target')-n('saved'));
  const required=n('months')>0?Math.ceil(remaining/n('months')):0;
  const budgetConfigured=n('income')>0||n('essential')>0||n('reserve')>0;
  const budgetNegative=n('income')<n('essential')+n('reserve');
  const short=n('monthly')>available;
  const enough=n('saved')+n('monthly')*n('months')>=n('target');
  const feasible=!short&&!budgetNegative&&enough;
  const validPlan=isWhole(form.target,1)&&isWhole(form.months,1,120)&&isWhole(form.saved||'0')&&isWhole(form.monthly);
  const previousEssential=state.budget.categories.reduce((sum,category)=>sum+category.amount,0);
  const replacesCategories=previousEssential>0&&n('essential')!==previousEssential;

  const set=(key:keyof Form)=>(value:string)=>{
    if(key==='monthly')monthlyEdited.current=true;
    setForm(previous=>{
      const next={...previous,[key]:value};
      if(['target','saved','months'].includes(key)&&!monthlyEdited.current) {
        next.monthly=isWhole(next.target,1)&&isWhole(next.saved||'0')&&isWhole(next.months,1,120)
          ?String(Math.ceil(Math.max(0,Number(next.target)-Number(next.saved))/Number(next.months))):'';
      }
      return next;
    });
    setErrors(previous=>({...previous,[key]:undefined}));
    setError(null);
  };

  async function save() {
    if(sending.current)return;
    const nextErrors:FormErrors={};
    if(!form.title.trim())nextErrors.title='Напиши, на что хочешь накопить.';
    for(const key of amountKeys) {
      const value=optionalKeys.some(optional=>optional===key)&&form[key]===''?'0':form[key];
      if(!isWhole(value))nextErrors[key]='Укажи целые рубли от 0 до 1 000 000.';
    }
    if(!nextErrors.target&&n('target')<1)nextErrors.target='Сумма цели должна быть больше нуля.';
    if(!isWhole(form.months,1,120))nextErrors.months='Укажи срок от 1 до 120 месяцев.';
    setErrors(nextErrors);
    if(Object.keys(nextErrors).length) {
      if(nextErrors.income||nextErrors.essential||nextErrors.reserve)setAdvanced(true);
      setError('Проверь поля с подсказками и сохрани ещё раз.');
      if(nextErrors.title||nextErrors.target||nextErrors.months)scroll.current?.scrollTo({y:0,animated:true});
      return;
    }
    sending.current=true;
    setBusy(true);
    onBusyChange(true);
    setError(null);
    Keyboard.dismiss();
    try {
      const input:GoalInput={title:form.title.trim(),target:n('target'),saved:n('saved'),months:n('months'),monthly:n('monthly'),income:n('income'),essential:n('essential'),reserve:n('reserve')};
      onSave(await api.goal(input));
    } catch(e) {
      setError((e as Error).message);
    } finally {
      sending.current=false;
      setBusy(false);
      onBusyChange(false);
    }
  }

  function fit() {
    const monthly=Math.min(available,required);
    const months=monthly>0?Math.min(120,Math.max(1,Math.ceil(remaining/monthly))):n('months');
    monthlyEdited.current=true;
    setForm(previous=>({...previous,monthly:String(monthly),months:String(months)}));
    setErrors(previous=>({...previous,monthly:undefined,months:undefined}));
    setError(null);
  }

  return <ScrollView ref={scroll} contentContainerStyle={[s.page,{gap:24,paddingBottom:48}]} keyboardShouldPersistTaps="handled" keyboardDismissMode="on-drag" showsVerticalScrollIndicator={false}>
    <View style={s.between}>
      <IconButton name="back" label="Вернуться к цели" onPress={onClose}/>
      <View style={{width:46}}/>
    </View>
    <View style={{gap:8}}>
      <Txt accessibilityRole="header" size={30} weight="extra" style={{letterSpacing:-.8,lineHeight:38}}>{state.goal?'Изменить цель':'Новая цель'}</Txt>
      <Txt color={colors.muted}>Укажи сумму и срок — рассчитаем ежемесячный взнос.</Txt>
    </View>
    {!state.goal&&<View style={{gap:10}}><Txt size={14} weight="bold">Идеи для цели · необязательно</Txt><View style={{flexDirection:'row',flexWrap:'wrap',gap:8}}>{[{title:'Наушники',amount:'12000'},{title:'Поездка',amount:'18000'}].map(item=><Pressable key={item.title} disabled={busy} accessibilityRole="button" accessibilityLabel={`Взять пример: ${item.title}`} onPress={()=>{set('title')(item.title);set('target')(item.amount);}} style={{minHeight:46,paddingHorizontal:15,borderRadius:15,backgroundColor:item.title==='Наушники'?colors.lavender:colors.mint,alignItems:'center',justifyContent:'center'}}><Txt size={14} weight="bold">{item.title}</Txt></Pressable>)}</View><Txt size={13} color={colors.muted}>Это примеры. Название и сумму можно изменить.</Txt></View>}
    <Card style={{gap:20}}>
      <FormField label="На что копим" value={form.title} onChangeText={set('title')} placeholder="Например, велосипед" maxLength={60} error={errors.title}/>
      <FormField label="Сумма цели, ₽" value={form.target} onChangeText={set('target')} numeric placeholder="Например, 15 000" maxLength={7} error={errors.target}/>
      <FormField label="Срок, месяцев" value={form.months} onChangeText={set('months')} numeric placeholder="Например, 6" maxLength={3} error={errors.months}/>
      <FormField label="Уже накоплено, ₽" value={form.saved} onChangeText={set('saved')} numeric hint="Если пока не откладывал, оставь 0." maxLength={7} error={errors.saved}/>
      <FormField label="Планирую откладывать в месяц, ₽" value={form.monthly} onChangeText={set('monthly')} numeric hint={monthlyEdited.current?'Взнос можно изменить в любой момент.':'Рассчитаем по сумме и сроку. Можно изменить.'} maxLength={7} error={errors.monthly}/>
    </Card>
    <View style={{gap:18}}>
      <Pressable accessibilityRole="button" disabled={busy} accessibilityState={{expanded:advanced}} aria-expanded={advanced} onPress={()=>setAdvanced(!advanced)} style={[s.between,{minHeight:48,gap:12}]}>
        <View style={{gap:3,flex:1}}>
          <Txt weight="bold" size={17}>Проверить по бюджету</Txt>
          <Txt size={13} color={colors.muted}>{budgetConfigured?`Свободно в месяц: ${formatMoney(available)}`:'Необязательно — можно добавить позже'}</Txt>
        </View>
        <Icon name={advanced?'close':'plus'} color={colors.blue}/>
      </Pressable>
      {advanced&&<Card style={{gap:20}}>
        <FormField label="Поступления в месяц, ₽" value={form.income} onChangeText={set('income')} numeric hint="Зарплата, стипендия или помощь близких." maxLength={7} error={errors.income}/>
        <FormField label="Обязательные траты в месяц, ₽" value={form.essential} onChangeText={set('essential')} numeric hint="Например, еда, проезд и жильё." maxLength={7} error={errors.essential}/>
        <FormField label="Запас на неожиданности, ₽" value={form.reserve} onChangeText={set('reserve')} numeric maxLength={7} error={errors.reserve}/>
        {replacesCategories&&<Txt size={13} color={colors.muted}>Новая общая сумма заменит разбивку расходов и попадёт в «Другое». Категории можно настроить в бюджете.</Txt>}
      </Card>}
    </View>
    {validPlan&&<View style={{gap:10,paddingTop:20,borderTopWidth:1,borderTopColor:colors.line}}>
      <Txt size={20} weight="bold">По сроку нужно {formatMoney(required)} в месяц</Txt>
      <Txt size={14} color={colors.muted}>{remaining===0?'Сумма цели уже накоплена.':!budgetConfigured?(enough?'Взнос покрывает цель. Добавь бюджет, чтобы проверить, насколько он посилен.':'Для этого срока нужен больший взнос. Можно увеличить срок или уменьшить сумму цели.'):budgetNegative?'Траты и запас превышают поступления. Проверь суммы в бюджете.':short?`Взнос выше свободных ${formatMoney(available)}. Можно уменьшить его и увеличить срок.`:enough?'Взнос покрывает цель и помещается в бюджет.':'Взнос пока не покрывает цель. Можно увеличить срок или уменьшить сумму цели.'}</Txt>
      {budgetConfigured&&!feasible&&available>0&&!budgetNegative&&remaining>0&&<Button label="Подстроить под бюджет" variant="secondary" icon="refresh" onPress={fit}/>}
      <Txt size={12} color={colors.muted}>Расчёт без процентов и изменения цен.</Txt>
    </View>}
    <View style={{gap:14}}>
      <ErrorNote message={error}/>
      <Button label={state.goal?'Сохранить изменения':'Создать цель'} onPress={save} loading={busy}/>
    </View>
  </ScrollView>;
}

export function CheckinScreen({onSave,onClose,onBusyChange,initialAmount}:{initialAmount?:number;onBusyChange:(v:boolean)=>void;onSave:(s:AppState)=>void;onClose:()=>void}) {
  const [type,setType]=useState<'saved'|'reviewed'>('saved');
  const [amount,setAmount]=useState(initialAmount?String(initialAmount):'');
  const [note,setNote]=useState('');
  const [errors,setErrors]=useState<{amount?:string;note?:string}>({});
  const [error,setError]=useState<string|null>(null);
  const [busy,setBusy]=useState(false);
  const sending=useRef(false);
  const key=useRef(`step_${Date.now()}_${Math.random().toString(36).slice(2)}`);
  const sent=useRef<{type:'saved'|'reviewed';amount?:number;note?:string}|null>(null);

  function mode(next:'saved'|'reviewed') {
    setType(next);
    setErrors({});
    setError(null);
    Keyboard.dismiss();
  }

  async function save() {
    if(sending.current)return;
    if(type==='saved'&&!isWhole(amount,1)) {
      setErrors({amount:'Укажи целую сумму от 1 до 1 000 000 ₽.'});
      return;
    }
    if(type==='reviewed'&&note.trim().length<3) {
      setErrors({note:'Запиши, что проверил или решил изменить. Минимум 3 символа.'});
      return;
    }
    sending.current=true;
    setBusy(true);
    onBusyChange(true);
    setErrors({});
    setError(null);
    Keyboard.dismiss();
    const payload={type,...(type==='saved'?{amount:Number(amount)}:{}),note:note.trim()};
    if(sent.current&&JSON.stringify(sent.current)!==JSON.stringify(payload))key.current=`step_${Date.now()}_${Math.random().toString(36).slice(2)}`;
    sent.current=payload;
    try {
      onSave(await api.checkin(payload,key.current));
    } catch(e) {
      setError((e as Error).message);
    } finally {
      sending.current=false;
      setBusy(false);
      onBusyChange(false);
    }
  }

  return <ScrollView contentContainerStyle={[s.page,{gap:24,paddingBottom:48}]} keyboardShouldPersistTaps="handled" keyboardDismissMode="on-drag" showsVerticalScrollIndicator={false}>
    <View style={s.between}><IconButton name="back" label="Вернуться к цели" onPress={onClose}/><View style={{width:46}}/></View>
    <View style={{gap:8}}>
      <Txt accessibilityRole="header" size={30} weight="extra" style={{letterSpacing:-.8,lineHeight:38}}>{type==='saved'?'Добавить накопление':'Пересмотр плана'}</Txt>
      <Txt color={colors.muted}>{type==='saved'?'Запиши сумму, которую уже отложил.':'Запиши, что проверил или решил изменить.'}</Txt>
    </View>
    <Card style={{gap:20}}>
      {type==='saved'&&<FormField label="Сумма накопления, ₽" numeric value={amount} onChangeText={value=>{setAmount(value);setErrors(previous=>({...previous,amount:undefined}));setError(null);}} placeholder="Например, 500" maxLength={7} error={errors.amount}/>}
      <FormField label={type==='saved'?'Заметка — необязательно':'Что проверил или изменил'} value={note} onChangeText={value=>{setNote(value);setErrors(previous=>({...previous,note:undefined}));setError(null);}} placeholder={type==='saved'?'Например, с первой подработки':'Например, увеличил срок на месяц'} maxLength={200} error={errors.note}/>
    </Card>
    <Txt size={13} color={colors.muted}>{type==='saved'?'Сумма добавится к накоплениям в твоей цели. Деньги никуда не переводятся.':'Сохраним заметку в истории. Чтобы изменить суммы или срок, открой редактирование цели.'}</Txt>
    <View style={{gap:14}}>
      <ErrorNote message={error}/>
      <Button label={type==='saved'?'Добавить накопление':'Сохранить заметку'} onPress={save} loading={busy}/>
    </View>
    <Pressable accessibilityRole="button" disabled={busy} onPress={()=>mode(type==='saved'?'reviewed':'saved')} style={{minHeight:48,flexDirection:'row',alignItems:'center',justifyContent:'center',gap:10,paddingVertical:12,borderTopWidth:1,borderTopColor:colors.line}}>
      <Icon name={type==='saved'?'refresh':'plus'} size={20} color={colors.blue}/>
      <Txt size={14} weight="bold" color={colors.blue}>{type==='saved'?'Пересмотреть план':'Добавить накопление'}</Txt>
    </Pressable>
  </ScrollView>;
}
