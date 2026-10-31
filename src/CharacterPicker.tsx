import React, { useRef, useState } from 'react';
import { Image, Pressable, ScrollView, View, useWindowDimensions } from 'react-native';
import { LinearGradient } from 'expo-linear-gradient';
import type { CharacterId } from '../shared/types';
import { characters, getCharacter } from './characters';
import { Button, colors, ErrorNote, Icon, IconButton, Mascot, Txt } from './ui';

type Props={initial?:CharacterId|null;motion:boolean;onSelect:(id:CharacterId)=>Promise<void>;onClose?:()=>void;onBusyChange?:(busy:boolean)=>void};
export function CharacterPicker({initial,motion,onSelect,onClose,onBusyChange}:Props){
 const [selected,setSelected]=useState<CharacterId>(initial||'lumi');
 const [busy,setBusy]=useState(false),[error,setError]=useState<string|null>(null);
 const sending=useRef(false);const compact=useWindowDimensions().height<760;
 const character=getCharacter(selected);
 async function save(id=selected){if(sending.current)return;sending.current=true;setBusy(true);onBusyChange?.(true);setError(null);try{await onSelect(id);}catch(e){setError((e as Error).message);}finally{sending.current=false;setBusy(false);onBusyChange?.(false);}}
 return <View style={{flex:1,backgroundColor:colors.bg}}>
  <ScrollView showsVerticalScrollIndicator={false} contentContainerStyle={{padding:20,paddingTop:12,paddingBottom:20,gap:20}}>
   {onClose&&<IconButton name="back" label="Вернуться без смены персонажа" onPress={()=>{if(!busy)onClose();}}/>}
   <View style={{gap:8}}><Txt accessibilityRole="header" size={29} weight="extra" style={{lineHeight:37,letterSpacing:-.7}}>Кто будет с тобой?</Txt><Txt size={14} color={colors.muted}>Выбери своего спутника. Сменить его можно в любой момент.</Txt></View>
   <LinearGradient colors={character.colors} start={{x:0,y:0}} end={{x:1,y:1}} style={{borderRadius:30,minHeight:compact?210:248,alignItems:'center',justifyContent:'center',padding:12,overflow:'hidden'}}>
    <View pointerEvents="none" style={{position:'absolute',width:190,height:190,borderRadius:95,backgroundColor:'#FFFFFF60'}}/>
    <Mascot character={selected} size={compact?156:184} animated={motion}/>
   </LinearGradient>
   <View style={{gap:7,alignItems:'center'}}><Txt size={25} weight="extra">{character.name}</Txt><Txt size={14} color={colors.muted}>{character.description}</Txt></View>
   <View accessibilityRole="radiogroup" accessibilityLabel="Выбор персонажа" style={{flexDirection:'row',gap:10}}>{characters.map(item=><Pressable key={item.id} disabled={busy} accessibilityRole="radio" accessibilityLabel={`Персонаж: ${item.name}`} accessibilityState={{checked:selected===item.id,disabled:busy}} aria-checked={selected===item.id} aria-disabled={busy} onPress={()=>{setSelected(item.id);setError(null);}} style={{flex:1,minHeight:126,borderRadius:22,padding:10,gap:8,alignItems:'center',justifyContent:'center',backgroundColor:selected===item.id?item.tint:'white',borderWidth:2,borderColor:selected===item.id?colors.blue:'transparent'}}><Image accessible={false} source={item.source} resizeMode="contain" style={{width:64,height:76}}/><View style={{flexDirection:'row',gap:4,alignItems:'center'}}><Txt size={13} weight="bold">{item.name}</Txt>{selected===item.id&&<Icon name="success" size={15} color={colors.blue}/>}</View></Pressable>)}</View>
  </ScrollView>
  <View style={{paddingHorizontal:20,paddingTop:10,paddingBottom:12,gap:10}}><ErrorNote message={error}/><Button label={`Выбрать: ${character.name}`} loading={busy} onPress={()=>save()}/>{!initial&&<Pressable disabled={busy} accessibilityRole="button" accessibilityLabel="Оставить Люми по умолчанию" onPress={()=>save('lumi')} style={{minHeight:44,alignItems:'center',justifyContent:'center'}}><Txt size={14} color={colors.muted}>Пока оставить Люми</Txt></Pressable>}</View>
 </View>;
}
