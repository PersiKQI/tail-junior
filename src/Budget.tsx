import { TAB_BAR_BOTTOM_SPACE } from './layout';
import React, { useMemo, useRef, useState } from 'react';
import { ActivityIndicator, Keyboard, Pressable, ScrollView, StyleSheet, TextInput, useWindowDimensions, View } from 'react-native';
import type { AppState, BudgetInput, BudgetCategoryId, Expense, ExpenseInput } from '../shared/types';
import { api } from './api';
import { Button, Card, colors, ErrorNote, Field, formatMoney, Icon, Mascot, ProgressBar, s, Txt, type IconName } from './ui';
import { budgetCategories, filterExpenses, localDateKey, parseRubles, summarizeExpenses, type ExpensePeriod } from './budgetModel';

type BudgetDraft={income:string;reserve:string;monthly:string;categories:Record<BudgetCategoryId,string>};
type ExpenseDraft={amount:string;category:BudgetCategoryId;occurredOn:string;note:string};
type BudgetFieldKey='income'|'reserve'|'monthly'|BudgetCategoryId;
const makeBudgetDraft=(b:AppState['budget']):BudgetDraft=>({income:String(b.income),reserve:String(b.reserve),monthly:String(b.monthly),categories:Object.fromEntries(b.categories.map(c=>[c.id,String(c.amount)])) as Record<BudgetCategoryId,string>});
const makeExpenseDraft=():ExpenseDraft=>({amount:'',category:'food',occurredOn:localDateKey(),note:''});
const safeNumber=(value:string)=>Number.isFinite(parseRubles(value))?parseRubles(value):0;
const titleDate=(date:string)=>new Intl.DateTimeFormat('ru-RU',{day:'numeric',month:'long',year:'numeric'}).format(new Date(`${date}T12:00:00`));
const expenseDateLabel=(date:string,today:string)=>{
 if(date===today)return 'Сегодня';
 const parsed=new Date(`${date}T12:00:00`);
 return /^\d{4}-\d{2}-\d{2}$/.test(date)&&Number.isFinite(parsed.getTime())&&localDateKey(parsed)===date?titleDate(date):'Проверь дату';
};
const periodTitles={month:'Этот месяц',week:'Эта неделя',all:'Всё время'};
const percent=(amount:number,total:number)=>total>0?Math.round(amount/total*100):0;

function SmallButton({label,onPress,disabled=false,selected,icon}:{label:string;onPress:()=>void;disabled?:boolean;selected?:boolean;icon?:IconName}) {
 return <Pressable accessibilityRole="button" accessibilityLabel={label} accessibilityState={{disabled,...(selected===undefined?{}:{selected})}} aria-disabled={disabled} aria-pressed={selected} disabled={disabled} onPress={onPress} style={({pressed})=>[styles.smallButton,{backgroundColor:selected?colors.ink:colors.paleBlue,opacity:disabled?.5:pressed?.75:1}]}>
  {icon&&<Icon name={icon} size={17} color={selected?'white':colors.deepBlue}/>}
  <Txt size={13} weight="bold" color={selected?'white':colors.deepBlue}>{label}</Txt>
 </Pressable>;
}

function BudgetField({error,...props}:React.ComponentProps<typeof Field>&{error?:string}) {
 return <View style={{gap:7}}><Field {...props}/>{error&&<Txt accessibilityRole="alert" size={12} color={colors.red}>{error}</Txt>}</View>;
}

function MoneyRow({label,amount}:{label:string;amount:number}) {
 return <View style={[s.between,{gap:16,minHeight:42}]}><Txt size={14} color={colors.muted} style={{flex:1}}>{label}</Txt><Txt size={15} weight="bold">{formatMoney(amount)}</Txt></View>;
}

function DisclosureRow({title,subtitle,open,onPress,disabled=false}:{title:string;subtitle?:string;open:boolean;onPress:()=>void;disabled?:boolean}) {
 return <Pressable accessibilityRole="button" accessibilityState={{expanded:open,disabled}} aria-expanded={open} disabled={disabled} onPress={onPress} style={[s.between,{gap:12,minHeight:48}]}>
  <View style={{gap:3,flex:1}}><Txt size={16} weight="bold">{title}</Txt>{subtitle&&<Txt size={13} color={colors.muted}>{subtitle}</Txt>}</View>
  <Icon name={open?'close':'plus'} size={20} color={colors.blue}/>
 </Pressable>;
}

function NavigationRow({title,description,icon,onPress,disabled}:{title:string;description:string;icon:IconName;onPress:()=>void;disabled:boolean}) {
 return <Pressable accessibilityRole="button" disabled={disabled} onPress={onPress} style={{minHeight:64,flexDirection:'row',alignItems:'center',gap:12,paddingVertical:12}}>
  <Icon name={icon} color={colors.deepBlue} size={22}/><View style={{flex:1,gap:3}}><Txt size={15} weight="bold">{title}</Txt><Txt size={13} color={colors.muted}>{description}</Txt></View><Icon name="chevron" size={19} color={colors.muted}/>
 </Pressable>;
}

export function BudgetScreen({state,onState,onLab,onBusyChange,onGoal,onActions}:{state:AppState;onState:(s:AppState)=>void;onLab:()=>void;onBusyChange:(b:boolean)=>void;onGoal?:()=>void;onActions?:()=>void}) {
 const [mode,setMode]=useState<'plan'|'expenses'>('plan');
 const [editing,setEditing]=useState(false);
 const [draft,setDraft]=useState<BudgetDraft>(()=>makeBudgetDraft(state.budget));
 const [expenseOpen,setExpenseOpen]=useState(false);
 const [expenseDraft,setExpenseDraft]=useState<ExpenseDraft>(makeExpenseDraft);
 const [expenseDetails,setExpenseDetails]=useState(false);
 const [categoriesOpen,setCategoriesOpen]=useState(false);
 const [comparisonOpen,setComparisonOpen]=useState(false);
 const [filtersOpen,setFiltersOpen]=useState(false);
 const [busy,setBusy]=useState(false);
 const [pendingExpense,setPendingExpense]=useState<string|null>(null);
 const [error,setError]=useState<string|null>(null);
 const [budgetErrors,setBudgetErrors]=useState<Partial<Record<BudgetFieldKey,string>>>({});
 const [expenseErrors,setExpenseErrors]=useState<{amount?:string;occurredOn?:string}>({});
 const [notice,setNotice]=useState<string|null>(null);
 const [period,setPeriod]=useState<ExpensePeriod>('month');
 const [category,setCategory]=useState<BudgetCategoryId|'all'>('all');
 const [showVoided,setShowVoided]=useState(false);
 const [visibleCount,setVisibleCount]=useState(20);
 const scroll=useRef<React.ComponentRef<typeof ScrollView>>(null);
 const working=useRef(false);
 const lastRequest=useRef<{signature:string;key:string}|null>(null);
 const narrow=useWindowDimensions().width<360;
 const expenses=state.expenses??[];
 const now=new Date();
 const today=localDateKey(now);
 const summary=useMemo(()=>summarizeExpenses(expenses,new Date()),[expenses,today]);
 const monthName=new Intl.DateTimeFormat('ru-RU',{month:'long',year:'numeric'}).format(now);
 const preview:BudgetInput=editing?{income:safeNumber(draft.income),reserve:safeNumber(draft.reserve),monthly:safeNumber(draft.monthly),categories:budgetCategories.map(c=>({id:c.id,title:c.title,amount:safeNumber(draft.categories[c.id])}))}:state.budget;
 const essential=preview.categories.reduce((sum,c)=>sum+c.amount,0);
 const free=preview.income-essential-preview.reserve-preview.monthly;
 const allocated=essential+preview.reserve+preview.monthly;
 const parts=[...preview.categories.map(c=>({id:c.id,amount:c.amount,color:budgetCategories.find(category=>category.id===c.id)!.color})),{id:'reserve',amount:preview.reserve,color:'#F1C451'},{id:'goal',amount:preview.monthly,color:colors.blue},{id:'free',amount:Math.max(0,free),color:'#A2C96C'}];
 const configured=state.budget.income>0||state.budget.reserve>0||state.budget.categories.some(c=>c.amount>0);
 const list=filterExpenses(expenses,period,category,showVoided,now);
 const visible=list.slice(0,visibleCount);
 const voidedCount=expenses.filter(expense=>expense.voidedAt).length;

 function setWorking(value:boolean) {working.current=value;setBusy(value);onBusyChange(value);}
 function showError(message:string) {setError(message);scroll.current?.scrollTo({y:0,animated:false});}
 function startEdit() {
  setDraft(makeBudgetDraft(state.budget));setEditing(true);setExpenseOpen(false);setMode('plan');setError(null);setBudgetErrors({});setNotice(null);scroll.current?.scrollTo({y:0,animated:false});
 }
 function openExpense() {
  setExpenseDraft(makeExpenseDraft());setExpenseDetails(false);setExpenseErrors({});lastRequest.current=null;setExpenseOpen(true);setEditing(false);setMode('expenses');setError(null);setNotice(null);scroll.current?.scrollTo({y:0,animated:false});
 }
 function switchMode(next:'plan'|'expenses') {if(busy)return;setMode(next);setError(null);setNotice(null);}
 function changeBudget(key:'income'|'reserve'|'monthly',value:string) {setDraft(previous=>({...previous,[key]:value}));setBudgetErrors(previous=>({...previous,[key]:undefined}));setError(null);}
 function changeCategory(key:BudgetCategoryId,value:string) {setDraft(previous=>({...previous,categories:{...previous.categories,[key]:value}}));setBudgetErrors(previous=>({...previous,[key]:undefined}));setError(null);}
 function changeExpense<K extends keyof ExpenseDraft>(key:K,value:ExpenseDraft[K]) {setExpenseDraft(previous=>({...previous,[key]:value}));setExpenseErrors(previous=>({...previous,[key]:undefined}));setError(null);}

 async function saveBudget() {
  if(working.current)return;
  const values:[BudgetFieldKey,string][]=[['income',draft.income],['reserve',draft.reserve],['monthly',draft.monthly],...budgetCategories.map(c=>[c.id,draft.categories[c.id]] as [BudgetFieldKey,string])];
  const nextErrors:Partial<Record<BudgetFieldKey,string>>={};
  for(const [key,value] of values) {const amount=parseRubles(value);if(!Number.isSafeInteger(amount)||amount<0||amount>1000000)nextErrors[key]='Укажи целые рубли от 0 до 1 000 000.';}
  setBudgetErrors(nextErrors);
  if(Object.keys(nextErrors).length) {showError('Проверь поля с подсказками и сохрани ещё раз.');return;}
  if(essential>1000000) {showError('Общие расходы по четырём категориям не должны превышать 1 000 000 ₽.');return;}
  setWorking(true);setError(null);Keyboard.dismiss();
  try {onState(await api.budget(preview));setEditing(false);setNotice(state.goal?'Бюджет сохранён. Расчёт цели обновился.':'Бюджет сохранён.');}
  catch(e) {showError((e as Error).message);}
  finally {setWorking(false);}
 }

 async function saveExpense() {
  if(working.current)return;
  const amount=parseRubles(expenseDraft.amount);
  if(!Number.isSafeInteger(amount)||amount<1||amount>1000000) {setExpenseErrors({amount:'Укажи целую сумму от 1 до 1 000 000 ₽.'});return;}
  const date=expenseDraft.occurredOn;
  const parsed=new Date(`${date}T12:00:00`);
  if(!/^\d{4}-\d{2}-\d{2}$/.test(date)||!Number.isFinite(parsed.getTime())||localDateKey(parsed)!==date||date<'1900-01-01'||date>today) {
   setExpenseDetails(true);setExpenseErrors({occurredOn:'Укажи существующую дату в формате ГГГГ-ММ-ДД: сегодня или раньше.'});return;
  }
  const payload:ExpenseInput={amount,category:expenseDraft.category,occurredOn:date,note:expenseDraft.note.trim(),timezoneOffsetMinutes:new Date().getTimezoneOffset()};
  const signature=JSON.stringify({amount:payload.amount,category:payload.category,occurredOn:payload.occurredOn,note:payload.note});
  if(lastRequest.current?.signature!==signature)lastRequest.current={signature,key:`expense-${Date.now()}-${Math.random().toString(36).slice(2)}`};
  setWorking(true);setError(null);Keyboard.dismiss();
  try {onState(await api.expense(payload,lastRequest.current.key));setExpenseOpen(false);setPeriod('all');setCategory('all');setVisibleCount(20);setNotice(`Расход ${formatMoney(amount)} записан. План и накопления не изменились.`);}
  catch(e) {showError((e as Error).message);}
  finally {setWorking(false);}
 }

 async function toggleExpense(expense:Expense) {
  if(working.current)return;
  setWorking(true);setPendingExpense(expense.id);setError(null);
  try {onState(await api.voidExpense(expense.id,!expense.voidedAt));setNotice(expense.voidedAt?'Запись восстановлена и снова входит в итоги.':'Запись отменена и исключена из итогов. Её можно восстановить.');}
  catch(e) {showError((e as Error).message);}
  finally {setPendingExpense(null);setWorking(false);}
 }

 function categoryFilter(value:BudgetCategoryId|'all') {setCategory(value);setVisibleCount(20);}
 function planNumber(label:string,key:'income'|'reserve'|'monthly',hint?:string) {
  return <BudgetField label={label} value={draft[key]} onChangeText={value=>changeBudget(key,value)} numeric maxLength={9} hint={hint} error={budgetErrors[key]}/>;
 }

 return <ScrollView ref={scroll} contentContainerStyle={[s.page,{paddingHorizontal:20,paddingBottom:TAB_BAR_BOTTOM_SPACE,gap:24}]} keyboardShouldPersistTaps="handled" keyboardDismissMode="on-drag" showsVerticalScrollIndicator={false}>
  <View style={[s.between,{gap:12}]}><View style={{gap:6,flex:1}}><Txt accessibilityRole="header" size={30} weight="extra" style={{letterSpacing:-.8,lineHeight:38}}>Бюджет</Txt><Txt size={14} color={colors.muted}>{monthName}</Txt></View>{!editing&&!expenseOpen&&<SmallButton label="Расход" icon="plus" onPress={openExpense} disabled={busy||expenses.length>=500}/>}</View>
  <View style={styles.tabs}>{([{id:'plan',label:'План'},{id:'expenses',label:'Расходы'}] as const).map(tab=><Pressable key={tab.id} accessibilityRole="tab" accessibilityLabel={tab.label} accessibilityState={{selected:mode===tab.id,disabled:busy}} aria-selected={mode===tab.id} disabled={busy} onPress={()=>switchMode(tab.id)} style={[styles.tab,{backgroundColor:mode===tab.id?'white':'transparent'}]}><Txt size={15} weight="bold" color={mode===tab.id?colors.ink:colors.muted}>{tab.label}</Txt></Pressable>)}</View>
  <ErrorNote message={error}/>
  {notice&&<View accessibilityLiveRegion="polite" aria-live="polite" style={{padding:16,backgroundColor:colors.mint,borderRadius:20,gap:12}}><Txt size={14} color={colors.green}>{notice}</Txt>{mode==='expenses'&&voidedCount>0&&!showVoided&&<SmallButton label="Показать отменённые" onPress={()=>{setShowVoided(true);setPeriod('all');setCategory('all');setFiltersOpen(true);}} disabled={busy}/>}</View>}

  {mode==='plan'&&<>
   {editing?<>
    <View style={s.between}><Txt accessibilityRole="header" size={22} weight="extra" style={{flex:1}}>Месячный план</Txt><SmallButton label="Отменить" onPress={()=>{setEditing(false);setBudgetErrors({});setError(null);}} disabled={busy}/></View>
    <Card style={{gap:20}}>
     {planNumber('Поступления за месяц, ₽','income','Стипендия, подработка и другие ожидаемые поступления.')}
     <View style={{gap:16,paddingTop:18,borderTopWidth:1,borderTopColor:colors.line}}>
      <Txt size={17} weight="bold">Обязательные расходы</Txt>
      {budgetCategories.map(c=><BudgetField key={c.id} label={`${c.title}, ₽ в месяц`} value={draft.categories[c.id]} numeric maxLength={9} onChangeText={value=>changeCategory(c.id,value)} error={budgetErrors[c.id]}/>)}
     </View>
     {planNumber('Запас на неожиданности, ₽','reserve')}
     {planNumber('Взнос на цель в месяц, ₽','monthly','Это план. Деньги никуда не переводятся.')}
    </Card>
    <View style={{gap:8}}><Txt size={18} weight="bold" color={free<0?colors.red:colors.ink}>{free<0?'Не хватает по плану: ':'Остаётся по плану: '}{formatMoney(Math.abs(free))}</Txt><Txt size={13} color={colors.muted}>{free<0?'Расходы, запас и взнос превышают поступления. Можно изменить суммы или сохранить план для дальнейшей работы.':'После обязательных расходов, запаса и взноса на цель.'}</Txt></View>
    <Button label="Сохранить план" onPress={saveBudget} loading={busy}/>
    {state.goal&&<Txt size={13} color={colors.muted}>Расчёт цели обновится. Уже накопленная сумма останется прежней.</Txt>}
   </>:!configured?<View style={{gap:18}}>
    <View style={[styles.planScene,{minHeight:158,backgroundColor:'#DDF3A8'}]}>
     <View style={{maxWidth:narrow?'59%':'63%',gap:8}}><Txt accessibilityRole="header" size={23} weight="extra" style={{lineHeight:30}}>Место для{ '\n'}твоих планов</Txt><Txt size={14}>Начни с поступлений и трат на месяц.</Txt></View>
     <Mascot size={narrow?116:128} animated={false} style={{position:'absolute',right:narrow?-12:0,bottom:-6}}/>
    </View>
    <Button label="Настроить бюджет" onPress={startEdit} icon="sliders"/>
    <Txt size={14} color={colors.muted}>Расходы можно записывать уже сейчас. Они появятся во вкладке «Расходы».</Txt>
   </View>:<>
    <View style={{gap:14}}>
     <View style={[styles.planScene,{backgroundColor:free<0?'#FFE0B8':'#DDF29C'}]}>
      <View style={{maxWidth:narrow?'60%':'62%',gap:9}}>
       <Txt size={14} weight="bold">{free<0?'Не хватает в плане':'Остаётся по плану'}</Txt>
       <Txt size={32} weight="extra" style={{letterSpacing:-.8}} adjustsFontSizeToFit minimumFontScale={.8} numberOfLines={1}>{formatMoney(Math.abs(free))}</Txt>
       <Txt size={14}>{free<0?'Проверь суммы — план можно изменить.':'После расходов, запаса и взноса на цель.'}</Txt>
      </View>
      <Mascot size={narrow?130:148} animated={false} style={{position:'absolute',right:narrow?-20:-8,bottom:-10}}/>
     </View>
     <Button label="Настроить план" variant="secondary" icon="sliders" onPress={startEdit}/>
    </View>
    <Card style={{padding:18,gap:7}}>
     <View accessibilityLabel={`Распределение месячного плана: ${formatMoney(allocated)}. ${free<0?`Не хватает ${formatMoney(-free)}.`:`Свободно ${formatMoney(free)}.`}`} style={{flexDirection:'row',height:12,gap:3,borderRadius:7,overflow:'hidden',marginBottom:8}}>{parts.filter(part=>part.amount>0).map(part=><View key={part.id} style={{flex:part.amount,backgroundColor:part.color,borderRadius:4}}/>)}</View>
     <MoneyRow label="Поступления за месяц" amount={preview.income}/>
     <View style={{gap:12}}>
      <Pressable accessibilityRole="button" accessibilityState={{expanded:categoriesOpen,disabled:busy}} aria-expanded={categoriesOpen} disabled={busy} onPress={()=>setCategoriesOpen(!categoriesOpen)} style={[s.between,{gap:10,minHeight:48}]}>
       <Txt size={14} color={colors.muted} style={{flex:1}}>Обязательные расходы</Txt><Txt size={15} weight="bold">{formatMoney(essential)}</Txt><Icon name={categoriesOpen?'close':'plus'} size={18} color={colors.blue}/>
      </Pressable>
      {categoriesOpen&&<View style={{gap:18,paddingTop:8,paddingBottom:14,borderTopWidth:1,borderTopColor:colors.line}}>{budgetCategories.map(c=>{const amount=preview.categories.find(category=>category.id===c.id)?.amount??0;const actual=summary.byCategory[c.id];return <View key={c.id} style={{gap:8}}>
       <View style={[s.between,{gap:10}]}><View style={{flexDirection:'row',alignItems:'center',gap:8,flex:1}}><Icon name={c.icon} size={18} color={c.color}/><Txt size={14} weight="bold">{c.title}</Txt></View><Txt size={15} weight="bold">{formatMoney(amount)}</Txt></View>
       <ProgressBar value={preview.income>0?amount/preview.income:0} color={c.color} background={c.tint} height={5}/>
       <Txt size={12} color={colors.muted}>{preview.income>0?`${percent(amount,preview.income)}% поступлений · `:''}Записано за месяц: {formatMoney(actual)}{actual>amount?' · выше плана':''}</Txt>
      </View>;})}</View>}
     </View>
     <MoneyRow label="Запас на неожиданности" amount={preview.reserve}/>
     <MoneyRow label="Взнос на цель" amount={preview.monthly}/>
     <View style={{borderTopWidth:1,borderTopColor:colors.line,paddingTop:12,gap:10}}>
      <SmallButton label="Открыть журнал расходов" icon="wallet" onPress={()=>{setEditing(false);setExpenseOpen(false);switchMode('expenses');scroll.current?.scrollTo({y:0,animated:false});}} disabled={busy}/>
      {onGoal&&<SmallButton label="Посмотреть цель" icon="flag" onPress={onGoal} disabled={busy}/>}
     </View>
    </Card>
   </>}
   {!editing&&<View style={{borderTopWidth:1,borderTopColor:colors.line,paddingTop:8}}>
    <NavigationRow title="Что если условия изменятся?" description="Попробовать другой вариант плана" icon="spark" onPress={onLab} disabled={busy}/>
    <NavigationRow title={onActions?'План на неделю':'Записать расход'} description={onActions?'Выбрать посильное действие':'Добавить трату в журнал'} icon={onActions?'calendar':'plus'} onPress={onActions??openExpense} disabled={busy}/>
   </View>}
  </>}

  {mode==='expenses'&&<>
   {expenses.length>=500&&<View style={{backgroundColor:colors.yellow,borderRadius:18,padding:16}}><Txt size={13}>Журнал заполнен: 500 записей. Их можно отменять и восстанавливать. Добавление новых недоступно в этой версии.</Txt></View>}
   {expenseOpen?<Card style={{gap:20,padding:20}}>
    <View style={s.between}><Txt accessibilityRole="header" size={23} weight="extra" style={{flex:1}}>Новый расход</Txt><Pressable accessibilityRole="button" accessibilityLabel="Закрыть ввод расхода" disabled={busy} onPress={()=>{setExpenseOpen(false);setExpenseErrors({});setError(null);Keyboard.dismiss();}} style={styles.close}><Icon name="close" size={20}/></Pressable></View>
    <BudgetField label="Сумма расхода, ₽" value={expenseDraft.amount} onChangeText={value=>changeExpense('amount',value)} numeric placeholder="Например, 350" maxLength={9} error={expenseErrors.amount}/>
    <View style={{flexDirection:'row',flexWrap:'wrap',gap:7}}>{[100,300,500,1000].map(amount=><SmallButton key={amount} label={`${amount} ₽`} onPress={()=>changeExpense('amount',String(amount))} selected={parseRubles(expenseDraft.amount)===amount} disabled={busy}/>)}</View>
    <View style={{gap:10}}><Txt size={13} weight="bold">Категория</Txt><View style={{flexDirection:'row',flexWrap:'wrap',gap:8}}>{budgetCategories.map(c=><Pressable key={c.id} accessibilityRole="radio" accessibilityLabel={c.title} accessibilityState={{checked:expenseDraft.category===c.id,disabled:busy}} aria-checked={expenseDraft.category===c.id} disabled={busy} onPress={()=>changeExpense('category',c.id)} style={{width:'48%',minHeight:50,borderRadius:16,backgroundColor:expenseDraft.category===c.id?colors.paleBlue:colors.bg,borderWidth:1.5,borderColor:expenseDraft.category===c.id?colors.blue:'transparent',flexDirection:'row',alignItems:'center',gap:8,paddingHorizontal:12}}><Icon name={c.icon} size={19} color={expenseDraft.category===c.id?colors.blue:colors.muted}/><Txt size={13} weight="bold" style={{flex:1}}>{c.title}</Txt>{expenseDraft.category===c.id&&<Icon name="check" size={15} color={colors.blue}/>}</Pressable>)}</View></View>
    <View style={{gap:18}}>
     <DisclosureRow title="Дата и заметка" subtitle={expenseDetails?undefined:expenseDateLabel(expenseDraft.occurredOn,today)+(expenseDraft.note.trim()?' · Есть заметка':'')} open={expenseDetails} onPress={()=>setExpenseDetails(!expenseDetails)} disabled={busy}/>
     {expenseDetails&&<>
      <BudgetField label="Дата расхода" value={expenseDraft.occurredOn} onChangeText={value=>changeExpense('occurredOn',value)} placeholder="ГГГГ-ММ-ДД" hint="Сегодня или прошедший день." maxLength={10} error={expenseErrors.occurredOn}/>
      <View style={{flexDirection:'row',gap:8}}><SmallButton label="Сегодня" selected={expenseDraft.occurredOn===today} disabled={busy} onPress={()=>changeExpense('occurredOn',today)}/><SmallButton label="Вчера" disabled={busy} onPress={()=>{const yesterday=new Date();yesterday.setDate(yesterday.getDate()-1);changeExpense('occurredOn',localDateKey(yesterday));}}/></View>
      <View style={{gap:7}}><Txt size={13} weight="bold">Заметка — необязательно</Txt><TextInput accessibilityLabel="Заметка к расходу" editable={!busy} value={expenseDraft.note} onChangeText={value=>changeExpense('note',value)} placeholder="Например, обед после учёбы" placeholderTextColor={colors.muted} multiline maxLength={200} style={[s.input,{fontSize:16,minHeight:88,textAlignVertical:'top'}]}/><Txt size={12} color={colors.muted}>{expenseDraft.note.length}/200</Txt></View>
     </>}
    </View>
    <Button label="Записать расход" onPress={saveExpense} loading={busy} icon="check"/>
    <Txt size={13} color={colors.muted}>Запись попадёт в журнал. План и накопления останутся прежними.</Txt>
   </Card>:<>
    {expenses.length===0?<Card style={{gap:16}}><Icon name="wallet" size={28} color={colors.blue}/><Txt accessibilityRole="header" size={24} weight="extra">Пока нет расходов</Txt><Txt color={colors.muted}>Запиши первую трату — например, обед или проезд. Бюджет настраивать заранее не нужно.</Txt><Button label="Добавить расход" onPress={openExpense} icon="plus" disabled={busy}/></Card>:<>
     <View style={{gap:8}}><Txt size={14} color={colors.muted}>Записано за этот месяц</Txt><Txt size={32} weight="extra" style={{letterSpacing:-.8}}>{formatMoney(summary.monthTotal)}</Txt><Txt size={13} color={colors.muted}>Записей: {summary.monthCount} · За неделю: {formatMoney(summary.weekTotal)}</Txt></View>
     <Button label="Добавить расход" onPress={openExpense} icon="plus" disabled={busy||expenses.length>=500}/>
     <View style={{gap:16,borderTopWidth:1,borderTopColor:colors.line,paddingTop:12}}>
      <DisclosureRow title="Сравнить с планом" subtitle="Только записанные расходы за месяц" open={comparisonOpen} onPress={()=>setComparisonOpen(!comparisonOpen)} disabled={busy}/>
      {comparisonOpen&&<Card style={{gap:20}}>{budgetCategories.map(c=>{const planned=state.budget.categories.find(x=>x.id===c.id)?.amount??0;const actual=summary.byCategory[c.id];const over=actual>planned;return <View key={c.id} style={{gap:8}}><Txt size={14} weight="bold">{c.title}</Txt><View style={s.between}><Txt size={13} color={colors.muted}>Записано / план</Txt><Txt size={13} weight="bold" color={over?colors.red:colors.ink}>{formatMoney(actual)} / {formatMoney(planned)}</Txt></View><ProgressBar value={planned>0?actual/planned:actual>0?1:0} color={over?colors.red:c.color} background={c.tint} height={6}/><Txt size={12} color={over?colors.red:colors.muted}>{planned===0?(actual>0?'Для категории пока нет плана':'План не задан'):over?`Выше плана на ${formatMoney(actual-planned)}`:`В плане осталось ${formatMoney(planned-actual)}`}</Txt></View>;})}</Card>}
     </View>
     <View style={{gap:14}}>
      <View style={s.between}><Txt accessibilityRole="header" size={22} weight="extra" style={{flex:1}}>Журнал расходов</Txt><SmallButton label="Фильтры" icon="sliders" onPress={()=>setFiltersOpen(!filtersOpen)} disabled={busy} selected={filtersOpen}/></View>
      <Txt size={13} color={colors.muted}>{periodTitles[period]} · {category==='all'?'Все категории':budgetCategories.find(c=>c.id===category)!.title}{showVoided?' · С отменёнными':''}</Txt>
      {filtersOpen&&<Card style={{gap:16,padding:18}}>
       <View style={{flexDirection:'row',flexWrap:'wrap',gap:7}}>{([{id:'month',label:'Месяц'},{id:'week',label:'Неделя'},{id:'all',label:'Всё время'}] as const).map(p=><SmallButton key={p.id} label={p.label} selected={period===p.id} disabled={busy} onPress={()=>{setPeriod(p.id);setVisibleCount(20);}}/>)}</View>
       <View style={{flexDirection:'row',flexWrap:'wrap',gap:7}}><SmallButton label="Все категории" selected={category==='all'} disabled={busy} onPress={()=>categoryFilter('all')}/>{budgetCategories.map(c=><SmallButton key={c.id} label={c.title} selected={category===c.id} disabled={busy} onPress={()=>categoryFilter(c.id)}/>)}</View>
       {voidedCount>0&&<Pressable accessibilityRole="switch" accessibilityLabel="Показывать отменённые расходы" accessibilityState={{checked:showVoided,disabled:busy}} aria-checked={showVoided} disabled={busy} onPress={()=>{setShowVoided(value=>!value);setVisibleCount(20);}} style={{minHeight:48,flexDirection:'row',alignItems:'center',gap:10}}><View style={{width:38,height:24,borderRadius:12,backgroundColor:showVoided?colors.blue:'#D7DDE9',padding:3,alignItems:showVoided?'flex-end':'flex-start'}}><View style={{width:18,height:18,borderRadius:9,backgroundColor:'white'}}/></View><Txt size={13} color={colors.muted} style={{flex:1}}>Отменённые записи · {voidedCount}</Txt></Pressable>}
      </Card>}
      {list.length===0?<Card style={{gap:14}}><Txt size={18} weight="bold">Нет записей по фильтру</Txt><Txt size={14} color={colors.muted}>Попробуй другой период или категорию.</Txt><SmallButton label="Показать все записи" onPress={()=>{setPeriod('all');setCategory('all');setShowVoided(voidedCount>0);setVisibleCount(20);}} disabled={busy}/></Card>:<View style={{gap:12}}>{visible.map((expense,index)=>{const c=budgetCategories.find(x=>x.id===expense.category)!;const newDate=index===0||visible[index-1].occurredOn!==expense.occurredOn;return <View key={expense.id} style={{gap:9}}>
       {newDate&&<Txt size={13} weight="bold" color={colors.muted} style={{marginTop:index?10:0}}>{expense.occurredOn===today?'Сегодня':titleDate(expense.occurredOn)}</Txt>}
       <View style={{backgroundColor:expense.voidedAt?'#F0F2F7':'white',borderRadius:22,padding:16,gap:12}}>
        <View style={{flexDirection:'row',alignItems:'center',gap:11}}><Icon name={c.icon} size={22} color={c.color}/><View style={{flex:1,gap:3}}><Txt size={14} weight="bold">{c.title}</Txt>{expense.voidedAt&&<Txt size={12} color={colors.muted}>Отменён · не входит в итоги</Txt>}</View><Txt size={17} weight="extra" color={expense.voidedAt?colors.muted:colors.ink} style={expense.voidedAt?{textDecorationLine:'line-through'}:{}}>{formatMoney(expense.amount)}</Txt></View>
        {expense.note!==''&&<Txt size={14} color={colors.muted}>{expense.note}</Txt>}
        <View style={{alignItems:'flex-end'}}><Pressable accessibilityRole="button" accessibilityLabel={`${expense.voidedAt?'Восстановить':'Отменить'} расход ${c.title}, ${formatMoney(expense.amount)}, ${titleDate(expense.occurredOn)}`} accessibilityState={{disabled:busy,busy:pendingExpense===expense.id}} aria-busy={pendingExpense===expense.id} disabled={busy} onPress={()=>toggleExpense(expense)} style={{minHeight:44,flexDirection:'row',gap:7,alignItems:'center',paddingHorizontal:8}}>{pendingExpense===expense.id?<ActivityIndicator size="small" color={colors.blue}/>:<Icon name={expense.voidedAt?'refresh':'close'} size={16} color={colors.deepBlue}/>}<Txt size={13} weight="bold" color={colors.deepBlue}>{expense.voidedAt?'Восстановить':'Отменить запись'}</Txt></Pressable></View>
       </View>
      </View>;})}</View>}
      {visibleCount<list.length&&<><Txt size={13} color={colors.muted} style={{textAlign:'center'}}>Показано {visible.length} из {list.length} записей</Txt><Button label={`Показать ещё ${Math.min(20,list.length-visibleCount)}`} onPress={()=>setVisibleCount(value=>value+20)} variant="secondary" icon={null} disabled={busy}/></>}
      <Txt size={12} color={colors.muted}>{expenses.length} / 500 записей, включая отменённые. Журнал содержит только траты, которые ты внёс вручную.</Txt>
     </View>
    </>}
   </>}
  </>}
 </ScrollView>;
}

const styles=StyleSheet.create({
 planScene:{minHeight:182,borderRadius:28,padding:22,overflow:'hidden',justifyContent:'center'},
 smallButton:{minHeight:44,paddingHorizontal:12,paddingVertical:10,borderRadius:14,flexDirection:'row',alignItems:'center',justifyContent:'center',gap:6},
 tabs:{backgroundColor:'#E8EDF5',borderRadius:20,padding:5,flexDirection:'row',gap:5},
 tab:{flex:1,minHeight:46,borderRadius:16,alignItems:'center',justifyContent:'center'},
 close:{width:44,height:44,borderRadius:22,backgroundColor:colors.bg,alignItems:'center',justifyContent:'center'},
});
