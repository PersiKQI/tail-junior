import { TAB_BAR_BOTTOM_SPACE } from './layout';
import React, { useContext, useRef, useState } from 'react';
import { Image, Linking, Pressable, ScrollView, View } from 'react-native';
import { LinearGradient } from 'expo-linear-gradient';
import type { AppState, Choice, InvestmentAllocation, InvestmentAnswerResult, InvestmentAssetId, InvestmentLabResult, InvestmentScenarioId } from '../shared/types';
import { DEFAULT_INVESTMENT_ALLOCATION, evaluatePortfolio, INVESTMENT_ASSETS, INVESTMENT_LAB_QUESTION, INVESTMENT_LAB_REASONS, INVESTMENT_MODEL_NOTICE, INVESTMENT_SCENARIOS } from '../shared/investment';
import { api } from './api';
import { instruments, investmentGlossary } from './investment-content';
import { BusyContext, Button, Card, Chip, colors, ErrorNote, Icon, IconButton, Mascot, ProgressBar, s, Txt } from './ui';

type Mode = 'learn' | 'tools' | 'portfolio';
type Props = {state:AppState;motion:boolean;onState:(v:AppState)=>void;onBusyChange:(v:boolean)=>void;onGoal:()=>void;initialLessonId?:string};
const palette = {cash:'#C0C9D8',bond:'#7699F1',stock:'#E9AB72',fund:'#B698DC'};
const money=(n:number)=>new Intl.NumberFormat('ru-RU',{maximumFractionDigits:2}).format(n)+' ₽';
const signed=(n:number)=>(n>0?'+':'')+money(n);

function Pill({label,selected,onPress}:{label:string;selected:boolean;onPress:()=>void}){
 const busy=useContext(BusyContext);
 return <Pressable accessibilityRole="button" accessibilityLabel={label} accessibilityState={{selected,disabled:busy}} aria-pressed={selected} disabled={busy} onPress={onPress} style={{minHeight:44,justifyContent:'center',paddingHorizontal:13,paddingVertical:11,borderRadius:15,backgroundColor:selected?colors.ink:'white'}}><Txt size={11} weight="bold" color={selected?'white':colors.muted}>{label}</Txt></Pressable>;
}
function Options({items,value,onChange}:{items:readonly Choice[];value:string|null;onChange:(v:string)=>void}){
 const busy=useContext(BusyContext);
 return <View style={{gap:10}}>{items.map((c,i)=><Pressable key={c.id} accessibilityRole="radio" accessibilityLabel={c.text} accessibilityState={{checked:value===c.id,disabled:busy}} aria-checked={value===c.id} disabled={busy} onPress={()=>onChange(c.id)} style={{backgroundColor:value===c.id?'#E7EEFF':'white',borderWidth:2,borderColor:value===c.id?colors.blue:'transparent',borderRadius:21,padding:15,flexDirection:'row',gap:11,alignItems:'center'}}><View style={{width:26,height:26,borderRadius:13,backgroundColor:value===c.id?colors.blue:colors.bg,alignItems:'center',justifyContent:'center'}}>{value===c.id?<Icon name="check" size={16} color="white"/>:<Txt size={11} weight="bold">{i+1}</Txt>}</View><Txt size={13} style={{flex:1,lineHeight:21}}>{c.text}</Txt></Pressable>)}</View>;
}
function Source({url,title='Материал Банка России'}:{url:string;title?:string}){
 const [error,setError]=useState(false);
 return <View><Pressable accessibilityRole="link" accessibilityLabel={title} onPress={()=>{setError(false);void Linking.openURL(url).catch(()=>setError(true));}} style={{minHeight:44,flexDirection:'row',alignItems:'center',gap:7}}><Icon name="book" size={15} color={colors.deepBlue}/><Txt size={13} weight="bold" color={colors.deepBlue} style={{flex:1}}>{title} ↗</Txt></Pressable>{error&&<Txt size={13} color={colors.red}>Не удалось открыть источник. Попробуй ещё раз.</Txt>}</View>;
}

export function InvestmentEntry({state,onPress,compact=false}:{state:AppState;onPress:()=>void;compact?:boolean}){
 const completed=state.investments?.progress.completedLessons||0;
 return <LinearGradient colors={['#4A4FC5','#5F7DE6']} start={{x:0,y:0}} end={{x:1,y:1}} style={{borderRadius:27,padding:20,gap:15,overflow:'hidden'}}>
  <View style={s.between}><Chip label="ИНВЕСТИЦИИ" color="#FFFFFF22" textColor="white" icon="chart"/><Txt size={11} color="#F0EDFF">{completed} / 6 историй</Txt></View>
  <View style={{flexDirection:'row',gap:8,alignItems:'center'}}><View style={{flex:1,gap:8}}><Txt size={compact?22:25} weight="extra" color="white" style={{lineHeight:31,letterSpacing:-.5}}>Что происходит{ '\n'}с деньгами дальше?</Txt><Txt size={13} color="#F0EDFF">Инструменты, риск и первый виртуальный портфель.</Txt></View>{!compact&&<View style={{width:63,height:76,borderRadius:23,backgroundColor:'#DDD4FF',alignItems:'center',justifyContent:'center',transform:[{rotate:'9deg'}]}}><Icon name="chart" size={37} color="#5542A6"/></View>}</View>
  <Button label="Исследовать инвестиции" onPress={onPress} variant="white" style={{minHeight:48,paddingVertical:12}}/>
 </LinearGradient>;
}

function Disclosure({label,expanded,onPress,children}:{label:string;expanded:boolean;onPress:()=>void;children:React.ReactNode}){
 const busy=useContext(BusyContext);
 return <View style={{borderTopWidth:1,borderTopColor:colors.line}}><Pressable accessibilityRole="button" accessibilityLabel={label} accessibilityState={{expanded,disabled:busy}} aria-expanded={expanded} disabled={busy} onPress={onPress} style={{minHeight:52,flexDirection:'row',alignItems:'center',gap:12}}><Txt size={14} weight="bold" style={{flex:1}}>{label}</Txt><Icon name={expanded?'close':'plus'} size={18} color={colors.muted}/></Pressable>{expanded&&<View style={{gap:13,paddingBottom:16}}>{children}</View>}</View>;
}

function InstrumentExplorer(){
 const [selected,setSelected]=useState('bond'),[compare,setCompare]=useState('deposit'),[term,setTerm]=useState<string|null>(null),[comparisonOpen,setComparisonOpen]=useState(false),[glossaryOpen,setGlossaryOpen]=useState(false),[moreOpen,setMoreOpen]=useState(false);
 const item=instruments.find(x=>x.id===selected)!,other=instruments.find(x=>x.id===compare)!;
 const ink=item.id==='stock'?'#8D512B':item.ink;
 function select(id:string){setSelected(id);setMoreOpen(false);if(compare===id)setCompare(instruments.find(x=>x.id!==id)!.id);}
 return <View style={{gap:18}}>
  <Txt size={13} color={colors.muted}>Выбери инструмент: что ты покупаешь и чем рискуешь.</Txt>
  <View style={{flexDirection:'row',flexWrap:'wrap',gap:7}}>{instruments.map(x=><Pill key={x.id} label={x.title} selected={x.id===selected} onPress={()=>select(x.id)}/>)}</View>
  <Card style={{backgroundColor:item.color,gap:15,padding:20}}>
   <View style={{flexDirection:'row',alignItems:'center',gap:12}}><View style={{flex:1,gap:5}}><Txt size={24} weight="extra">{item.title}</Txt><Txt size={13} color={ink}>{item.subtitle}</Txt></View><Icon name={item.icon} color={ink} size={28}/></View>
   <Txt size={14} style={{lineHeight:23}}>{item.meaning}</Txt>
   <View style={{gap:5}}><Txt size={13} weight="bold" color={ink}>Риск</Txt><Txt size={13} style={{lineHeight:22}}>{item.risk}</Txt></View>
   <Txt size={13} color={ink} style={{lineHeight:22}}>{item.protection}</Txt>
   <Disclosure label="Доход, доступ к деньгам и источник" expanded={moreOpen} onPress={()=>setMoreOpen(!moreOpen)}><View style={{gap:6}}><Txt size={13} weight="bold">Откуда результат</Txt><Txt size={13} style={{lineHeight:22}}>{item.result}</Txt></View><View style={{gap:6}}><Txt size={13} weight="bold">Когда можно забрать деньги</Txt><Txt size={13} style={{lineHeight:22}}>{item.access}</Txt></View><Txt size={13} color={ink} style={{lineHeight:22}}>{item.question}</Txt><Source url={item.source}/></Disclosure>
  </Card>
  <View>
   <Disclosure label="Сравнить с другим инструментом" expanded={comparisonOpen} onPress={()=>setComparisonOpen(!comparisonOpen)}>
    <Txt size={13} color={colors.muted}>Сейчас: {item.title}. С чем сравнить?</Txt><View style={{flexDirection:'row',flexWrap:'wrap',gap:7}}>{instruments.filter(x=>x.id!==selected).map(x=><Pill key={x.id} label={x.title} selected={x.id===compare} onPress={()=>setCompare(x.id)}/>)}</View>
    {([{label:'Что может пойти не так',key:'risk'},{label:'Когда можно забрать деньги',key:'access'},{label:'Защита денег',key:'protection'}] as const).map(row=><View key={row.key} style={{gap:11,paddingVertical:8}}><Txt size={15} weight="extra">{row.label}</Txt>{[item,other].map(x=><View key={x.id} style={{gap:4}}><Txt size={13} weight="bold" color={x.ink}>{x.title}</Txt><Txt size={13} style={{lineHeight:22}}>{x[row.key]}</Txt></View>)}</View>)}
   </Disclosure>
   <Disclosure label="Словарь инвестиционных терминов" expanded={glossaryOpen} onPress={()=>setGlossaryOpen(!glossaryOpen)}>{investmentGlossary.map(x=><Disclosure key={x.term} label={x.term} expanded={term===x.term} onPress={()=>setTerm(term===x.term?null:x.term)}><Txt size={13} color={colors.muted} style={{lineHeight:22}}>{x.meaning}</Txt></Disclosure>)}</Disclosure>
  </View>
 </View>;
}

function PortfolioLab({state,onState,onBusyChange,onStep}:{state:AppState;onState:(v:AppState)=>void;onBusyChange:(v:boolean)=>void;onStep:()=>void}){
 const lab=state.investments.lab;
 const [allocation,setAllocation]=useState<InvestmentAllocation>({...lab.allocation}),[scenario,setScenario]=useState<InvestmentScenarioId>(lab.lastScenarioId||'down'),[reason,setReason]=useState<string|null>(null),[busy,setBusy]=useState(false),[error,setError]=useState<string|null>(null),[result,setResult]=useState<InvestmentLabResult|null>(null),[details,setDetails]=useState(false),[modelOpen,setModelOpen]=useState(false),[reviewedOpen,setReviewedOpen]=useState(false),[stage,setStage]=useState(0);
 const chosen=INVESTMENT_SCENARIOS.find(x=>x.id===scenario)!,outcome=evaluatePortfolio(allocation,scenario);
 const changed=JSON.stringify(allocation)!==JSON.stringify(lab.allocation)||scenario!==lab.lastScenarioId||(reason!==null&&reason!==lab.lastReasonId);
 function goTo(next:number){setStage(next);setError(null);onStep();}
 function change(id:Exclude<InvestmentAssetId,'cash'>,delta:number){if(busy)return;setAllocation(a=>({...a,[id]:a[id]+delta,cash:a.cash-delta}));setResult(null);setReason(null);}
 async function submit(){if(!reason)return;setBusy(true);onBusyChange(true);setError(null);try{const r=await api.investmentLab({allocation,scenarioId:scenario,reasonId:reason});onState(r.state);setResult(r);}catch(e){setError((e as Error).message);}finally{setBusy(false);onBusyChange(false);}}
 return <View style={{gap:20}}>
  <View style={{gap:12}}>
   <View accessibilityRole="tablist" style={{flexDirection:'row',gap:5}}>{['Состав','Результат','Объяснение'].map((label,i)=><Pressable key={label} accessibilityRole="tab" accessibilityLabel={`Шаг ${i+1}: ${label}`} accessibilityState={{selected:stage===i,disabled:i>stage||busy}} aria-selected={stage===i} disabled={i>stage||busy} onPress={()=>goTo(i)} style={{flex:1,minHeight:44,justifyContent:'center',gap:7}}><View style={{height:4,borderRadius:2,backgroundColor:i<=stage?'#8A72BB':colors.line}}/><Txt size={11} weight={stage===i?'extra':'regular'} color={stage===i?colors.ink:colors.muted}>{i+1}. {label}</Txt></Pressable>)}</View>
   <Txt size={13} color={colors.muted} style={{lineHeight:21}}>10 000 виртуальных ₽. Личные накопления и бюджет не меняются.</Txt>
  </View>
  {stage===0&&<View style={{gap:17}}>
   <View style={{gap:6}}><Txt accessibilityRole="header" size={22} weight="extra">Собери состав</Txt><Txt size={13} color={colors.muted}>Меняй доли по 1 000 ₽. Можно оставить всё без вложений.</Txt></View>
   <Card style={{padding:18,gap:18}}>
    <View style={[s.between,{gap:12}]}><Txt size={13} color={colors.muted} style={{flex:1}}>Без вложений</Txt><Txt size={22} weight="extra">{money(allocation.cash*1000)}</Txt></View>
    <View accessibilityLabel="Распределение учебной суммы" style={{height:10,flexDirection:'row',gap:3,borderRadius:6,overflow:'hidden'}}>{INVESTMENT_ASSETS.filter(x=>allocation[x.id]>0).map(x=><View key={x.id} style={{flex:allocation[x.id],backgroundColor:palette[x.id],borderRadius:4}}/>)}</View>
    {INVESTMENT_ASSETS.filter(x=>x.id!=='cash').map(x=><View key={x.id} style={{gap:8,borderTopWidth:1,borderTopColor:colors.line,paddingTop:15}}>
     <View style={[s.between,{gap:8}]}><Txt size={15} weight="bold" style={{flex:1}}>{x.title}</Txt><Txt size={15} weight="extra">{money(allocation[x.id]*1000)}</Txt></View>
     <Txt size={13} color={colors.muted}>{x.description}</Txt>
     <View style={{flexDirection:'row',gap:12,alignItems:'center'}}>{([-1,1] as const).map(delta=>{const disabled=busy||(delta<0?allocation[x.id]===0:allocation.cash===0);return <Pressable key={delta} accessibilityRole="button" accessibilityLabel={`${delta<0?'Уменьшить':'Увеличить'} ${x.title.toLocaleLowerCase('ru-RU')} на 1 000 виртуальных рублей`} accessibilityState={{disabled}} aria-disabled={disabled} disabled={disabled} onPress={()=>change(x.id as Exclude<InvestmentAssetId,'cash'>,delta)} style={{width:48,height:46,borderRadius:15,backgroundColor:disabled?colors.bg:'#E9EEFC',alignItems:'center',justifyContent:'center',opacity:disabled?.45:1}}><Txt size={23} weight="bold">{delta<0?'−':'+'}</Txt></Pressable>;})}<Txt size={11} color={colors.muted}>по 1 000 ₽</Txt></View>
    </View>)}
    <Pressable accessibilityRole="button" accessibilityLabel="Вернуть всё в свободные деньги" disabled={busy} onPress={()=>{setAllocation({...DEFAULT_INVESTMENT_ALLOCATION});setResult(null);setReason(null);}} style={{minHeight:44,justifyContent:'center'}}><Txt size={13} weight="bold" color={colors.deepBlue}>Вернуть всё без вложений</Txt></Pressable>
    <Txt size={13} color={colors.muted}>Деньги без вложений в модели не приносят процент.</Txt>
   </Card>
   <Button label="Посмотреть результат" onPress={()=>goTo(1)}/>
  </View>}
  {stage===1&&<View style={{gap:17}}>
   <View style={{gap:6}}><Txt accessibilityRole="header" size={22} weight="extra">Выбери ситуацию</Txt><Txt size={13} color={colors.muted}>Состав один, условия разные. Это вымышленные сценарии.</Txt></View>
   <View style={{gap:10}}>{INVESTMENT_SCENARIOS.map(x=>{const r=evaluatePortfolio(allocation,x.id);return <Pressable key={x.id} accessibilityRole="radio" accessibilityLabel={`${x.title}, ${x.months} месяца, после условной продажи ${money(r.ending)}`} accessibilityState={{checked:scenario===x.id,disabled:busy}} aria-checked={scenario===x.id} disabled={busy} onPress={()=>{setScenario(x.id);setResult(null);setReason(null);setDetails(false);}} style={{borderWidth:2,borderColor:scenario===x.id?'#7D71CA':'transparent',borderRadius:21,padding:16,backgroundColor:scenario===x.id?'#EEE9FC':'white',gap:12}}><View style={[s.between,{gap:10}]}><View style={{flex:1,gap:4}}><Txt size={15} weight="extra">{x.title}</Txt><Txt size={11} color={colors.muted}>{x.months} мес. · условная продажа</Txt></View><Icon name={scenario===x.id?'success':'chevron'} size={20} color="#75639A"/></View><View style={[s.between,{gap:12}]}><Txt size={20} weight="extra">{money(r.ending)}</Txt><Txt size={13} weight="bold" color={r.profit<0?colors.red:colors.ink}>{signed(r.profit)}</Txt></View></Pressable>;})}</View>
   <Disclosure label="Из чего получился результат" expanded={details} onPress={()=>setDetails(!details)}>
    <Txt size={13} color={colors.muted} style={{lineHeight:22}}>{chosen.description}</Txt>
    {[{label:'В начале',value:money(outcome.initial)},{label:'Изменение цены',value:signed(outcome.priceChange)},{label:'Купоны и выплаты',value:signed(outcome.cashIncome)},{label:'Комиссии',value:(outcome.fees?'−':'')+money(outcome.fees)},{label:'После условной продажи',value:money(outcome.ending)}].map(x=><View key={x.label} style={[s.between,{gap:12}]}><Txt size={13} color={colors.muted} style={{flex:1}}>{x.label}</Txt><Txt size={13} weight="bold">{x.value}</Txt></View>)}
    {outcome.rows.filter(r=>r.units>0).map(r=><View key={r.asset} style={{backgroundColor:'white',borderRadius:16,padding:14,gap:7}}><View style={[s.between,{gap:10}]}><Txt size={13} weight="bold" style={{flex:1}}>{INVESTMENT_ASSETS.find(x=>x.id===r.asset)?.title}</Txt><Txt size={13} weight="bold">{money(r.ending)}</Txt></View><Txt size={13} color={colors.muted} style={{lineHeight:21}}>Вложение {money(r.initial)} · цена {signed(r.priceChange)} · выплаты {signed(r.cashIncome)} · комиссии {money(r.fees)}</Txt></View>)}
   </Disclosure>
   <Button label="Объяснить результат" onPress={()=>goTo(2)}/><Button label="Изменить состав" variant="secondary" icon="back" onPress={()=>goTo(0)}/>
  </View>}
  {stage===2&&<View style={{gap:17}}>
   <View style={{gap:6}}><Txt accessibilityRole="header" size={22} weight="extra">Объясни результат</Txt><Txt size={13} color={colors.muted}>{chosen.title} · после условной продажи {money(outcome.ending)}</Txt></View>
   <Txt size={15} weight="bold">{INVESTMENT_LAB_QUESTION}</Txt><Options items={INVESTMENT_LAB_REASONS} value={reason} onChange={v=>{setReason(v);setResult(null);}}/><ErrorNote message={error}/>
   {result?<View accessibilityLiveRegion="polite" aria-live="polite" style={{backgroundColor:result.correct?colors.mint:colors.yellow,borderRadius:23,padding:18,gap:10}}><Txt size={18} weight="extra">{result.title}</Txt><Txt size={13} style={{lineHeight:22}}>{result.explanation}</Txt><Txt size={13} color={colors.muted}>Учебный вариант сохранён. Результат не прибавляется к твоей цели.</Txt><Button label="Проверить другой сценарий" variant="white" onPress={()=>{setResult(null);setReason(null);goTo(1);}}/></View>:<Button label="Сохранить и разобрать" onPress={submit} disabled={!reason} loading={busy}/>}
   <Button label="Вернуться к результату" variant="secondary" icon="back" onPress={()=>goTo(1)}/>
   <Txt size={13} color={colors.muted}>{changed?'Этот вариант ещё не сохранён.':lab.updatedAt?'Последний учебный вариант сохранён в профиле.':'Состав и объяснение сохранятся после разбора.'}</Txt>
  </View>}
  <View style={{gap:5}}><Txt size={13} color={colors.muted} style={{lineHeight:21}}>Возможен убыток. Это не прогноз. Налоги, инфляция, дефолты и накопленный купонный доход не рассчитаны.</Txt><Disclosure label="Как устроена учебная модель" expanded={modelOpen} onPress={()=>setModelOpen(!modelOpen)}><Txt size={13} color={colors.muted} style={{lineHeight:22}}>{INVESTMENT_MODEL_NOTICE}</Txt></Disclosure>
   {lab.reviewedScenarioIds.length>0&&<Disclosure label={`Сохранённые разборы · ${lab.reviewedScenarioIds.length} из 3`} expanded={reviewedOpen} onPress={()=>setReviewedOpen(!reviewedOpen)}>{INVESTMENT_SCENARIOS.map(x=><View key={x.id} style={{flexDirection:'row',gap:9,alignItems:'center'}}><Icon name={lab.reviewedScenarioIds.includes(x.id)?'check':'clock'} size={17} color={colors.muted}/><Txt size={13} color={colors.muted} style={{flex:1}}>{x.title}</Txt></View>)}<Txt size={13} color={colors.muted}>Это история сохранений, а не оценка знаний или рейтинг доходности.</Txt></Disclosure>}
  </View>
 </View>;
}

export function Investments({state,motion,onState,onBusyChange,onGoal,initialLessonId}:Props){
 const busy=useContext(BusyContext);
 const [mode,setMode]=useState<Mode>('learn'),[lessonId,setLessonId]=useState<string|null>(initialLessonId??null),[lessonsOpen,setLessonsOpen]=useState(true),[goalOpen,setGoalOpen]=useState(false);
 const scroll=useRef<React.ComponentRef<typeof ScrollView>>(null);
 const data=state.investments;
 if(!data)return <View style={s.page}><Icon name="chart" color={colors.blue} size={30}/><Txt>Не удалось загрузить инвестиционный маршрут. Обнови приложение.</Txt></View>;
 if(lessonId)return <InvestmentLessonView key={lessonId} id={lessonId} {...{state,motion,onState,onBusyChange}} onClose={()=>setLessonId(null)} onNext={id=>{setLessonId(id);if(!id)setMode('portfolio');}}/>;
 const next=data.lessons.find(x=>x.id===data.progress.nextLessonId),count=data.progress.completedLessons;
 return <ScrollView ref={scroll} showsVerticalScrollIndicator={false} keyboardShouldPersistTaps="handled" contentContainerStyle={{paddingHorizontal:22,paddingTop:17,paddingBottom:TAB_BAR_BOTTOM_SPACE,gap:16}}>
  <LinearGradient colors={['#373DA7','#4B5FC5','#7798E6']} start={{x:0,y:0}} end={{x:1,y:1}} style={{minHeight:138,borderRadius:28,padding:20,overflow:'hidden'}}>
   <Txt accessibilityRole="header" size={26} weight="extra" color="white" style={{letterSpacing:-.6}}>Инвестиции</Txt><Txt size={13} color="white" style={{width:'62%',lineHeight:21,marginTop:7}}>Риск, инструменты и виртуальный портфель.</Txt><Image accessibilityIgnoresInvertColors accessible={false} source={require('../assets/lumi-friends.png')} resizeMode="contain" style={{position:'absolute',width:119,height:96,right:-6,bottom:-8}}/>
  </LinearGradient>
  <View accessibilityRole="tablist" style={{flexDirection:'row',gap:8}}>{([{id:'learn',label:'Разобраться',icon:'book',color:'#E3D9FC',ink:'#67478C'},{id:'tools',label:'Инструменты',icon:'chart',color:'#DDEAFF',ink:'#315B9C'},{id:'portfolio',label:'Портфель',icon:'sliders',color:'#E3EDC9',ink:'#4E6C2C'}] as const).map(x=><Pressable key={x.id} disabled={busy} accessibilityRole="tab" accessibilityLabel={`Инвестиции: ${x.label}`} accessibilityState={{selected:mode===x.id,disabled:busy}} aria-selected={mode===x.id} onPress={()=>{setMode(x.id);scroll.current?.scrollTo({y:0,animated:false});}} style={{flex:1,minHeight:64,paddingHorizontal:3,paddingVertical:10,gap:6,borderWidth:2,borderColor:mode===x.id?x.ink:'transparent',borderRadius:19,backgroundColor:x.color,alignItems:'center',justifyContent:'center'}}><Icon name={x.icon} size={21} color={x.ink}/><Txt size={11} weight="extra" color={x.ink}>{x.label}</Txt></Pressable>)}</View>
  {mode==='learn'&&<View style={{gap:20}}>
   <Card style={{padding:17,gap:11,backgroundColor:'#E6D9FF'}}><View style={[s.between,{gap:12}]}><Txt size={13} weight="bold" color="#69517D">{next?'Следующая история':'Все истории разобраны'}</Txt><Txt size={11} color="#69517D">{count} из 6</Txt></View><Txt size={22} weight="extra" style={{letterSpacing:-.4}}>{next?.title||'Проверим идеи в деле?'}</Txt><Txt size={13} color="#69517D">{next?.subtitle||'Попробуй один портфель в разных условиях. Доходность здесь не оценивается.'}</Txt><ProgressBar value={count/6} color="#8A72BB" height={5} background="#D2BFEF"/><Button label={next?`Начать · ${next.duration}`:'Открыть виртуальный портфель'} onPress={()=>next?setLessonId(next.id):setMode('portfolio')}/></Card>
   <View><Disclosure label={`Все истории · ${count} из 6 разобрано`} expanded={lessonsOpen} onPress={()=>setLessonsOpen(!lessonsOpen)}>
    {data.lessons.map(item=><Pressable key={item.id} accessibilityRole="button" accessibilityLabel={`${item.completed?'Повторить':item.unlocked?'Открыть':'Закрытая история'}: ${item.title}`} accessibilityState={{disabled:!item.unlocked||busy}} aria-disabled={!item.unlocked||busy} disabled={!item.unlocked||busy} onPress={()=>setLessonId(item.id)} style={{flexDirection:'row',gap:12,paddingVertical:14,alignItems:'center',borderBottomWidth:1,borderBottomColor:colors.line}}><View style={{width:40,height:44,borderRadius:15,backgroundColor:item.completed?'#E1F1D5':item.unlocked?'#E9E2F7':colors.bg,alignItems:'center',justifyContent:'center'}}>{item.completed||!item.unlocked?<Icon name={item.completed?'check':'lock'} color={item.completed?'#587C40':colors.muted} size={20}/>:<Txt weight="extra" size={16}>{item.order}</Txt>}</View><View style={{flex:1,gap:5}}><Txt size={14} weight="bold" color={item.unlocked?colors.ink:colors.muted}>{item.title}</Txt><Txt size={11} color={colors.muted}>{item.completed?'Разобрано · можно повторить':item.unlocked?item.duration:`После истории ${item.order-1}`}</Txt></View>{item.unlocked&&<Icon name="chevron" size={16}/>}</Pressable>)}<Txt size={13} color={colors.muted}>«Разобрано» означает ответ с объяснением. Это ещё не доказательство устойчивого навыка.</Txt>
   </Disclosure>
   <Disclosure label="Как связать инвестиции со своей целью" expanded={goalOpen} onPress={()=>setGoalOpen(!goalOpen)}><Txt size={13} color={colors.muted} style={{lineHeight:22}}>Покупка через месяц и далёкая цель — разные задачи. Сумма, срок и возможность переждать убыток важнее красивого процента. Оставить деньги без инвестиций — тоже решение.</Txt><Button label="Открыть свою цель" variant="secondary" onPress={onGoal}/></Disclosure></View>
  </View>}
  {mode==='tools'&&<InstrumentExplorer/>}
  {mode==='portfolio'&&<PortfolioLab state={state} onState={onState} onBusyChange={onBusyChange} onStep={()=>scroll.current?.scrollTo({y:0,animated:false})}/>}
  <Txt size={13} color={colors.muted} style={{lineHeight:21}}>Учебный режим. Покупок и банковского подключения здесь нет; примеры не выбирают вложения за тебя.</Txt>
 </ScrollView>;
}

function InvestmentLessonView({id,state,motion,onState,onBusyChange,onClose,onNext}:{id:string;state:AppState;motion:boolean;onState:(v:AppState)=>void;onBusyChange:(v:boolean)=>void;onClose:()=>void;onNext:(id:string|null)=>void}){
 const item=state.investments.lessons.find(x=>x.id===id)!;
 const [stage,setStage]=useState(0),[choice,setChoice]=useState<string|null>(null),[reason,setReason]=useState<string|null>(null),[result,setResult]=useState<InvestmentAnswerResult|null>(null),[busy,setBusy]=useState(false),[error,setError]=useState<string|null>(null);
 async function submit(){if(!choice||!reason)return;setBusy(true);onBusyChange(true);setError(null);try{const r=await api.investmentAnswer(id,choice,reason);setResult(r);onState(r.state);}catch(e){setError((e as Error).message);}finally{setBusy(false);onBusyChange(false);}}
 if(result)return <ScrollView contentContainerStyle={[s.page,{paddingHorizontal:22,paddingBottom:TAB_BAR_BOTTOM_SPACE,gap:23}]}><View style={s.between}><Chip label={result.correct?'ЕСТЬ ОБЪЯСНЕНИЕ':'РАЗБЕРЁМСЯ ВМЕСТЕ'} color={result.correct?colors.mint:colors.yellow}/><IconButton name="close" label="Закрыть инвестиционный разбор" onPress={onClose}/></View><View style={{flexDirection:'row',gap:12,alignItems:'center'}}><Txt size={26} weight="extra" style={{flex:1,lineHeight:34,letterSpacing:-.6}}>{result.title}</Txt><Mascot size={64} animated={motion}/></View><Card style={{gap:13}}><Icon name="book" color={colors.blue}/><Txt size={15} style={{lineHeight:26}}>{result.explanation}</Txt></Card><Txt size={13} color={colors.muted}>{result.alreadyCompleted?'Повтор сохранён отдельно. Первая попытка не изменилась.':'Ответ и объяснение сохранены. К этой истории можно вернуться.'}</Txt><Button label={result.state.investments.progress.nextLessonId?'К следующей истории':'Попробовать портфель'} onPress={()=>onNext(result.state.investments.progress.nextLessonId)}/><Button label="Вернуться к инвестициям" variant="secondary" onPress={onClose}/></ScrollView>;
 return <ScrollView key={stage} contentContainerStyle={[s.page,{paddingHorizontal:22,paddingBottom:TAB_BAR_BOTTOM_SPACE,gap:23}]}><View style={{flexDirection:'row',gap:15,alignItems:'center'}}><IconButton name="back" label={stage===0?'Вернуться к инвестициям':'На шаг назад'} onPress={()=>stage===0?onClose():setStage(stage-1)}/><View style={{flex:1}}><ProgressBar value={(stage+1)/3} color="#9780D5"/></View><Txt size={11} color={colors.muted}>{stage+1} / 3</Txt></View><Chip label={`ИСТОРИЯ ${item.order} · ${item.duration}`} icon="book"/><Txt size={29} weight="extra" style={{letterSpacing:-.7,lineHeight:37}}>{stage===0?item.title:stage===1?item.question:'Почему именно так?'}</Txt>
  {stage===0?<><Card style={{backgroundColor:'#E9E4F9',gap:12}}><Txt size={13} weight="extra" color="#745E96">ОДНА ВАЖНАЯ МЫСЛЬ</Txt><Txt size={16} style={{lineHeight:27}}>{item.concept}</Txt></Card><Card style={{gap:10}}><Txt size={13} weight="extra" color={colors.deepBlue}>ПРОВЕРИМ НА ПРИМЕРЕ</Txt><Txt size={15} style={{lineHeight:25}}>{item.example}</Txt></Card>{item.sources.map(x=><Source key={x.url} url={x.url} title={x.title}/>)}<Button label="Попробовать решить" onPress={()=>setStage(1)}/></>:stage===1?<><Txt size={14} color={colors.muted} style={{lineHeight:23}}>{item.example}</Txt><Options items={item.choices} value={choice} onChange={setChoice}/><Button label="Объяснить выбор" disabled={!choice} onPress={()=>setStage(2)}/></>:<><View style={{backgroundColor:'#E9E4F9',borderRadius:20,padding:16,gap:6}}><Txt size={11} color="#745E96">Твой выбор</Txt><Txt size={14} weight="bold">{item.choices.find(x=>x.id===choice)?.text}</Txt></View><Options items={item.reasons} value={reason} onChange={setReason}/><ErrorNote message={error}/><Button label="Проверить объяснение" onPress={submit} disabled={!reason} loading={busy}/></>}
  <Txt size={13} color={colors.muted}>Условная ситуация. Здесь можно ошибаться и пересматривать решение.</Txt>
 </ScrollView>;
}
